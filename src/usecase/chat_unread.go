package usecase

import (
	"context"
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

	lastTS, lastKey, lastID := service.lastMessageRange(deviceIDFromContext(ctx), targetJID, lid)
	patch := buildMarkChatAsReadPatch(indexJIDs, !unread, lastTS, lastKey)

	if err = client.SendAppState(ctx, patch); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"chat_jid":   request.ChatJID,
			"index_jids": indexJIDs,
			"unread":     unread,
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
	}).Info("Chat unread operation completed successfully")

	return response, nil
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
