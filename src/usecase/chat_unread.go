package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	domainChatStorage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chatstorage"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/validations"
	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// UnreadChat marks a chat as unread (or read) on the account's own devices.
//
// Fork-specific. It is the phone's "Mark as unread" entry: an app state
// patch on the `regular_low` collection (index `markChatAsRead`, the one
// pin/archive live in) built with appstate.BuildMarkChatAsRead. The range it
// carries points at the newest message stored for the chat so the phone can
// place the marker; without a stored message the range is "now" with no
// key, which whatsmeow accepts.
//
// It is not a read receipt: `POST /message/{id}/read` tells the sender the
// message was read, this one only toggles the unread marker the phone shows
// in its chat list. Both can be combined: send the receipts, then mark the
// chat unread so a human finds it in the "Unread" filter.
func (service serviceChat) UnreadChat(ctx context.Context, request domainChat.UnreadChatRequest) (response domainChat.UnreadChatResponse, err error) {
	if err = validations.ValidateUnreadChat(ctx, &request); err != nil {
		return response, err
	}
	unread := request.Unread == nil || *request.Unread
	form := request.JIDForm
	if form == "" {
		form = domainChat.UnreadJIDFormPN
	}

	client := whatsapp.ClientFromContext(ctx)
	if client == nil {
		return response, pkgError.ErrWaCLI
	}

	// Validate JID and ensure connection (resolves an @lid input to its PN)
	targetJID, err := utils.ValidateJidWithLogin(client, request.ChatJID)
	if err != nil {
		return response, err
	}
	targetJID = targetJID.ToNonAD()

	// SendAppState reads the collection version without whatsmeow's sync
	// lock; take the per-device app state write mutex (shared with the
	// label writes and GET /labels full syncs) so two writes never encode
	// against the same version.
	state := whatsapp.LabelStateForClient(client)
	state.Lock()
	defer state.Unlock()

	lid := whatsapp.LIDForPN(ctx, targetJID, client)
	indexJIDs := unreadChatIndexJIDs(targetJID, lid, form)

	deviceID := deviceIDFromContext(ctx)
	lastTS, lastKey, lastID := service.lastMessageRange(deviceID, targetJID, lid)
	patch := buildMarkChatAsReadPatch(indexJIDs, !unread, lastTS, lastKey)

	repair, err := writeAppStateWithRepair(ctx,
		appStateRepairOptions{ForceResync: request.Resync, ForceRecover: request.Recover},
		func(ctx context.Context) error { return client.SendAppState(ctx, patch) },
		func(ctx context.Context) error {
			previous, err := whatsapp.FullSyncAppState(ctx, client, appstate.WAPatchRegularLow)
			logrus.WithError(err).WithFields(logrus.Fields{
				"device_id":        deviceID,
				"collection":       appstate.WAPatchRegularLow,
				"previous_version": previous,
				"chat_jid":         request.ChatJID,
			}).Info("Full resync of app state collection for mark chat as read")
			return err
		},
		func(ctx context.Context) error {
			version, err := whatsapp.RequestAppStateRecovery(ctx, client, deviceID, appstate.WAPatchRegularLow)
			logrus.WithError(err).WithFields(logrus.Fields{
				"device_id":  deviceID,
				"collection": appstate.WAPatchRegularLow,
				"version":    version,
				"chat_jid":   request.ChatJID,
			}).Info("App state recovery from the main phone for mark chat as read")
			return err
		})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"chat_jid":   request.ChatJID,
			"index_jids": indexJIDs,
			"unread":     unread,
			"resynced":   repair.Resynced,
			"recovered":  repair.Recovered,
		}).Error("Failed to send mark chat as read app state")
		return response, err
	}

	if lastTS.IsZero() {
		lastTS = time.Now()
	}

	response.Status = "success"
	response.ChatJID = targetJID.String()
	for _, jid := range indexJIDs {
		if jid.Server == "lid" {
			response.ChatLID = jid.String()
		}
	}
	response.Unread = unread
	response.LastMessageTimestamp = lastTS.UTC().Format(time.RFC3339)
	response.LastMessageID = lastID
	response.Resynced = repair.Resynced
	response.Recovered = repair.Recovered
	if unread {
		response.Message = "Chat marked as unread successfully"
	} else {
		response.Message = "Chat marked as read successfully"
	}

	logrus.WithFields(logrus.Fields{
		"chat_jid":        response.ChatJID,
		"chat_lid":        response.ChatLID,
		"unread":          unread,
		"last_message_id": lastID,
		"resynced":        repair.Resynced,
		"recovered":       repair.Recovered,
	}).Info("Chat unread operation completed successfully")

	return response, nil
}

// appStateRepairOptions are the manual repairs a request can force before
// the write (body / query flags `resync` and `recover`).
type appStateRepairOptions struct {
	ForceResync  bool
	ForceRecover bool
}

// appStateRepairResult reports which repairs ran during a write.
type appStateRepairResult struct {
	Resynced  bool
	Recovered bool
}

// writeAppStateWithRepair runs send and repairs the collection when the
// server rejects the write because the local copy drifted (409 /
// mismatching LTHash, see whatsapp.IsAppStateOutOfSync), in the order the
// whatsmeow maintainer recommends (tulir/whatsmeow#858):
//
//  1. full sync of the collection (resync), then one retry of send;
//  2. if the full sync itself fails LTHash verification, or the retry is
//     still rejected as out of sync, a recovery copy from the main phone
//     (recover), then one last retry of send.
//
// ForceResync / ForceRecover run the matching repair before the first
// send (manual repair); a repair that already ran is never repeated in the
// same call. The final error is returned whole, as whatsmeow built it, with
// the failed repair appended, since that text is what tells a drifted
// collection from any other rejection.
func writeAppStateWithRepair(ctx context.Context, opts appStateRepairOptions, send, resync, recover func(context.Context) error) (result appStateRepairResult, err error) {
	var resyncErr error
	if opts.ForceResync {
		if resyncErr = resync(ctx); resyncErr == nil {
			result.Resynced = true
		} else if !opts.ForceRecover || !errors.Is(resyncErr, appstate.ErrMismatchingLTHash) {
			return result, resyncErr
		}
		// else: the forced recovery below is exactly what repairs a full
		// sync that does not verify.
	}
	if opts.ForceRecover {
		if err = recover(ctx); err != nil {
			if resyncErr != nil {
				err = fmt.Errorf("%w (also, the app state recovery failed: %w)", resyncErr, err)
			}
			return result, err
		}
		result.Recovered = true
	}

	err = send(ctx)
	if err == nil || !whatsapp.IsAppStateOutOfSync(err) {
		return result, err
	}

	if !result.Resynced && !result.Recovered {
		logrus.WithError(err).Warn("App state write rejected as out of sync, full-syncing the collection and retrying once")
		if resyncErr = resync(ctx); resyncErr != nil {
			err = fmt.Errorf("%w (also, the full resync of the collection failed: %w)", err, resyncErr)
			if !errors.Is(resyncErr, appstate.ErrMismatchingLTHash) {
				return result, err
			}
			// The server's own snapshot does not verify: only the phone's
			// copy can fix that.
		} else {
			result.Resynced = true
			if err = send(ctx); err == nil || !whatsapp.IsAppStateOutOfSync(err) {
				return result, err
			}
		}
	}
	if result.Recovered {
		return result, err
	}

	logrus.WithError(err).Warn("App state collection still out of sync after the full sync, asking the main phone for a recovery copy and retrying once")
	if recoverErr := recover(ctx); recoverErr != nil {
		return result, fmt.Errorf("%w (also, the app state recovery failed: %w)", err, recoverErr)
	}
	result.Recovered = true
	return result, send(ctx)
}

// unreadChatIndexJIDs picks the `markChatAsRead` index keys to write for the
// requested form. Without a known LID every form degrades to the number.
func unreadChatIndexJIDs(pn, lid types.JID, form string) []types.JID {
	if lid.IsEmpty() {
		return []types.JID{pn}
	}
	switch form {
	case domainChat.UnreadJIDFormLID:
		return []types.JID{lid}
	case domainChat.UnreadJIDFormBoth:
		return []types.JID{lid, pn}
	default:
		return []types.JID{pn}
	}
}

// buildMarkChatAsReadPatch is appstate.BuildMarkChatAsRead for several index
// keys in one patch (same collection, one mutation per key).
func buildMarkChatAsReadPatch(indexJIDs []types.JID, read bool, lastTS time.Time, lastKey *waCommon.MessageKey) appstate.PatchInfo {
	patch := appstate.PatchInfo{Type: appstate.WAPatchRegularLow}
	for _, jid := range indexJIDs {
		single := appstate.BuildMarkChatAsRead(jid, read, lastTS, lastKey)
		patch.Mutations = append(patch.Mutations, single.Mutations...)
	}
	return patch
}

// lastMessageRange returns the newest stored message of the chat for this
// device as (timestamp, key, id). It looks the chat up by its phone-number
// JID first and by its LID second (history-synced chats may be stored
// either way). Zero / nil / "" when the chat has no stored message or there
// is no chat storage.
func (service serviceChat) lastMessageRange(deviceID string, pn, lid types.JID) (time.Time, *waCommon.MessageKey, string) {
	if service.chatStorageRepo == nil || deviceID == "" {
		return time.Time{}, nil, ""
	}
	candidates := []types.JID{pn}
	if !lid.IsEmpty() {
		candidates = append(candidates, lid)
	}
	for _, chatJID := range candidates {
		messages, err := service.chatStorageRepo.GetMessages(&domainChatStorage.MessageFilter{
			DeviceID: deviceID,
			ChatJID:  chatJID.String(),
			Limit:    1,
		})
		if err != nil {
			logrus.WithError(err).WithField("chat_jid", chatJID.String()).Warn("Failed to look up the chat's last message")
			continue
		}
		if len(messages) == 0 || messages[0] == nil {
			continue
		}
		return messages[0].Timestamp, lastMessageKey(messages[0]), messages[0].ID
	}
	return time.Time{}, nil, ""
}

// lastMessageKey builds the waCommon.MessageKey of a stored message.
func lastMessageKey(message *domainChatStorage.Message) *waCommon.MessageKey {
	key := &waCommon.MessageKey{
		RemoteJID: proto.String(message.ChatJID),
		FromMe:    proto.Bool(message.IsFromMe),
		ID:        proto.String(message.ID),
	}
	if chat, err := types.ParseJID(message.ChatJID); err == nil && chat.Server == types.GroupServer && !message.IsFromMe && message.Sender != "" {
		key.Participant = proto.String(message.Sender)
	}
	return key
}
