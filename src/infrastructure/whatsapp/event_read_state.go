package whatsapp

import (
	"context"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
)

const eventTypeChatReadState = "chat.read_state"

// Fork-specific: `chat.read_state` webhook from whatsmeow's typed
// *events.MarkChatAsRead, which the phone (or another linked device) emits
// when a whole chat is marked as read or unread from the chat list. Like the
// label events, full-sync replays are skipped. Payload: {chat_id, chat_lid?,
// read, last_message_timestamp?, last_message_id?}; the envelope `timestamp`
// is the time of the marking.
//
// Not emitted for read receipts (those are `message.ack`). It IS emitted
// for the mutations this instance writes through POST /chat/{jid}/unread:
// whatsmeow resyncs the collection after SendAppState and dispatches the
// echoed mutation, one event per index key written (same as the label
// events after POST /chat/{jid}/label).

func handleMarkChatAsRead(ctx context.Context, evt *events.MarkChatAsRead, deviceID string, client *whatsmeow.Client) {
	if evt == nil || evt.FromFullSync {
		return
	}
	payload := buildChatReadStatePayload(ctx, evt, client)
	if payload == nil {
		return
	}
	ts := labelstate.NormalizeEventTime(evt.Timestamp)
	forwardAppStateEventAsync(ctx, eventTypeChatReadState, payload, ts, deviceID)
}

// buildChatReadStatePayload returns the `chat.read_state` payload, or nil
// when the event carries nothing forwardable.
func buildChatReadStatePayload(ctx context.Context, evt *events.MarkChatAsRead, client *whatsmeow.Client) map[string]any {
	if evt == nil || evt.Action == nil || evt.JID.IsEmpty() {
		return nil
	}
	payload := map[string]any{
		"read": evt.Action.GetRead(),
	}
	addLabelChatFields(ctx, client, payload, evt.JID)
	if rng := evt.Action.GetMessageRange(); rng != nil {
		if secs := rng.GetLastMessageTimestamp(); secs > 0 {
			payload["last_message_timestamp"] = time.Unix(secs, 0).UTC().Format(time.RFC3339)
		}
		if msgs := rng.GetMessages(); len(msgs) > 0 && msgs[len(msgs)-1].GetKey() != nil {
			if id := msgs[len(msgs)-1].GetKey().GetID(); id != "" {
				payload["last_message_id"] = id
			}
		}
	}
	return payload
}
