package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/config"
	domainMessage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/message"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/mediaretry"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const (
	defaultMediaRetryTimeout = 30 * time.Second
	// maxMediaRetryTimeout caps the caller-supplied timeout_seconds.
	maxMediaRetryTimeout = 120 * time.Second
	// mediaRetryDownloadTimeout is the budget for downloading from the
	// refreshed DirectPath once the notification has arrived.
	mediaRetryDownloadTimeout = 60 * time.Second
)

// MediaRetry implements domainMessage.IMessageManagement.MediaRetry.
//
// It re-uses the chat storage the regular DownloadMedia path relies on to
// obtain the original MediaKey / FileSHA256 / FileEncSHA256 / FileLength
// needed by both SendMediaRetryReceipt and (post-retry) DownloadMediaWithPath.
// The heavy lifting is:
//  1. Register a waiter keyed by message_id.
//  2. Send the retry receipt.
//  3. Block until the whatsmeow event loop dispatches events.MediaRetry
//     (or timeout).
//  4. Decrypt the notification, download from the new DirectPath, persist
//     under the same storages/media/<chat>/<date>/ layout used by
//     DownloadMedia.
func (service serviceMessage) MediaRetry(ctx context.Context, request domainMessage.MediaRetryRequest) (response domainMessage.MediaRetryResponse, err error) {
	if request.MessageID == "" {
		return response, pkgError.ValidationError("message_id is required")
	}
	if request.Phone == "" {
		return response, pkgError.ValidationError("phone is required")
	}

	client := whatsapp.ClientFromContext(ctx)
	if client == nil {
		return response, pkgError.ErrWaCLI
	}

	dataWaRecipient, err := utils.ValidateJidWithLogin(client, request.Phone)
	if err != nil {
		return response, err
	}

	stored, err := service.chatStorageRepo.GetMessageByID(request.MessageID)
	if err != nil {
		return response, fmt.Errorf("message not found: %v", err)
	}
	if stored == nil {
		return response, fmt.Errorf("message with ID %s not found", request.MessageID)
	}
	if stored.MediaType == "" {
		return response, fmt.Errorf("message %s has no media to retry", request.MessageID)
	}
	if len(stored.MediaKey) == 0 {
		return response, fmt.Errorf("message %s has no stored mediaKey; cannot retry", request.MessageID)
	}
	if stored.ChatJID != dataWaRecipient.String() {
		return response, fmt.Errorf("message %s does not belong to chat %s", request.MessageID, dataWaRecipient.String())
	}

	mediaType, mmsType, ok := mapMediaType(stored.MediaType)
	if !ok {
		return response, fmt.Errorf("unsupported media type for retry: %s", stored.MediaType)
	}

	// Rebuild the minimum MessageInfo whatsmeow needs for the retry
	// receipt. The sender may be absent (is_from_me messages), in which
	// case we use the device's own JID so the protocol still resolves.
	info := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     dataWaRecipient,
			IsFromMe: stored.IsFromMe,
		},
		ID:        stored.ID,
		Timestamp: stored.Timestamp,
	}
	if stored.IsFromMe {
		if client.Store != nil && client.Store.ID != nil {
			info.Sender = *client.Store.ID
		} else {
			info.Sender = dataWaRecipient
		}
	} else {
		if stored.Sender != "" {
			senderJID, parseErr := utils.ParseJID(stored.Sender)
			if parseErr != nil {
				return response, fmt.Errorf("invalid stored sender JID %q: %v", stored.Sender, parseErr)
			}
			info.Sender = senderJID
		} else {
			// For 1:1 chats the sender is the chat JID when it isn't us.
			info.Sender = dataWaRecipient
		}
	}

	timeout := defaultMediaRetryTimeout
	if request.TimeoutSeconds > 0 {
		timeout = time.Duration(min(request.TimeoutSeconds, int(maxMediaRetryTimeout/time.Second))) * time.Second
	}

	// The global RequestTimeout middleware gives every request 45s, which
	// the wait plus the download can exceed. Detach from that deadline
	// (context values such as the device are kept) and apply our own budget.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout+mediaRetryDownloadTimeout)
	defer cancel()

	waiter := mediaretry.Register(request.MessageID)
	defer waiter.Release()

	if err := client.SendMediaRetryReceipt(ctx, &info, stored.MediaKey); err != nil {
		return response, fmt.Errorf("failed to send media retry receipt: %w", err)
	}

	var retryEvt *events.MediaRetry
	select {
	case retryEvt = <-waiter.Ch:
	case <-ctx.Done():
		return response, ctx.Err()
	case <-time.After(timeout):
		return response, fmt.Errorf("no media retry notification received within %s", timeout)
	}

	if retryEvt.Error != nil {
		return response, fmt.Errorf("media retry refused by sender (code %d)", retryEvt.Error.Code)
	}

	notif, err := whatsmeow.DecryptMediaRetryNotification(retryEvt, stored.MediaKey)
	if err != nil {
		return response, fmt.Errorf("failed to decrypt media retry notification: %w", err)
	}
	if notif == nil {
		return response, errors.New("empty media retry notification")
	}
	// The sender may still signal failure (GENERAL_ERROR, NOT_FOUND,
	// DECRYPTION_ERROR). Only SUCCESS plus a non-empty DirectPath means
	// we can actually re-download.
	newDirectPath := notif.GetDirectPath()
	if result := notif.GetResult(); result.String() != "SUCCESS" {
		return response, fmt.Errorf("media retry result %q, direct_path=%q", result.String(), newDirectPath)
	}
	if newDirectPath == "" {
		return response, errors.New("media retry notification had no new direct path")
	}

	// Download from the refreshed DirectPath. allowNoHash=false: we have
	// the original FileSHA256, so whatsmeow verifies the plaintext hash.
	data, err := client.DownloadMediaWithPath(
		ctx,
		newDirectPath,
		stored.FileEncSHA256,
		stored.FileSHA256,
		stored.MediaKey,
		mediaType,
		mmsType,
		false,
	)
	if err != nil {
		return response, fmt.Errorf("failed to download media via new path: %w", err)
	}

	maxFileSize := config.WhatsappSettingMaxDownloadSize
	if int64(len(data)) > maxFileSize {
		return response, fmt.Errorf("file size exceeds the maximum limit of %d bytes", maxFileSize)
	}

	// Persist with the same directory scheme DownloadMedia uses so
	// downstream tooling does not need to special-case retries.
	chatDir := filepath.Join(config.PathMedia, utils.ExtractPhoneNumber(stored.ChatJID))
	dateDir := filepath.Join(chatDir, stored.Timestamp.Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0755); err != nil {
		return response, fmt.Errorf("failed to create directory: %v", err)
	}

	ext := mediaExtensionFor(stored.MediaType, stored.Filename)
	filename := fmt.Sprintf("%d-retry-%s%s", time.Now().Unix(), stored.ID, ext)
	fullPath := filepath.Join(dateDir, filename)
	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return response, fmt.Errorf("failed to write media file: %v", err)
	}

	response.MessageID = request.MessageID
	response.Status = fmt.Sprintf("Media retry downloaded successfully to %s", fullPath)
	response.MediaType = stored.MediaType
	response.Filename = filepath.Base(fullPath)
	response.FilePath = fullPath
	response.FileSize = int64(len(data))

	logrus.Info(map[string]any{
		"message_id":   request.MessageID,
		"phone":        request.Phone,
		"chat":         dataWaRecipient.String(),
		"media_type":   response.MediaType,
		"file_path":    response.FilePath,
		"file_size":    response.FileSize,
		"direct_path":  newDirectPath,
		"event":        "media_retry_success",
		"timeout_secs": int(timeout.Seconds()),
	})

	return response, nil
}

// mapMediaType translates the string media_type persisted in chat storage
// to whatsmeow's MediaType constant and the URL mmsType segment required
// by DownloadMediaWithPath.
func mapMediaType(kind string) (whatsmeow.MediaType, string, bool) {
	switch kind {
	case "image":
		return whatsmeow.MediaImage, "image", true
	case "video", "video_note":
		// video_note is a PTV (round video message); same media namespace.
		return whatsmeow.MediaVideo, "video", true
	case "audio":
		return whatsmeow.MediaAudio, "audio", true
	case "document":
		return whatsmeow.MediaDocument, "document", true
	case "sticker":
		// Stickers share the image key derivation namespace.
		return whatsmeow.MediaImage, "image", true
	default:
		return "", "", false
	}
}

// mediaExtensionFor picks a reasonable file extension based on the stored
// media_type and (for documents) the original filename. The richer
// MIME-based logic lives in utils.ExtractMedia — here we only have raw
// bytes plus stored metadata, so a best-effort fallback is enough.
func mediaExtensionFor(kind, originalFilename string) string {
	if originalFilename != "" {
		if ext := filepath.Ext(originalFilename); ext != "" {
			return ext
		}
	}
	switch kind {
	case "image":
		return ".jpg"
	case "video", "video_note":
		return ".mp4"
	case "audio":
		return ".ogg"
	case "sticker":
		return ".webp"
	case "document":
		return ""
	default:
		return ""
	}
}
