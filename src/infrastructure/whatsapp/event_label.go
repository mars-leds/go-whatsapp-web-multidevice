package whatsapp

import (
	"context"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const (
	eventTypeLabelEdit        = "label.edit"
	eventTypeLabelAssociation = "label.association"
)

// Fork-specific: label webhooks are built from whatsmeow's typed events
// (*events.LabelEdit / *events.LabelAssociationChat) instead of the raw
// *events.AppState upstream used, so that (a) full-sync replays (which
// GET /labels triggers) can be skipped via FromFullSync and (b) the same
// events feed the per-client label cache. The webhook payloads are byte-for-
// byte what upstream emitted: `label.edit` {label_id, name, color,
// predefined_id, deleted, order_index, is_active, type, is_immutable,
// mute_end_time_ms} and `label.association` {label_id, labeled, chat_id,
// chat_lid?}.

func handleLabelEdit(ctx context.Context, evt *events.LabelEdit, deviceID string, client *whatsmeow.Client) {
	if evt == nil || evt.FromFullSync {
		return
	}
	ts := labelstate.NormalizeEventTime(evt.Timestamp)
	if client != nil {
		LabelStateForClient(client).ApplyLabelEdit(evt.LabelID, evt.Action, ts)
	}

	payload := buildLabelEditPayload(evt)
	if payload == nil {
		return
	}
	forwardAppStateEventAsync(ctx, eventTypeLabelEdit, payload, ts, deviceID)
}

func handleLabelAssociation(ctx context.Context, evt *events.LabelAssociationChat, deviceID string, client *whatsmeow.Client) {
	if evt == nil || evt.FromFullSync {
		return
	}
	ts := labelstate.NormalizeEventTime(evt.Timestamp)
	if client != nil && evt.Action != nil && !evt.JID.IsEmpty() {
		LabelStateForClient(client).ApplyAssociation(evt.JID.String(), evt.LabelID, evt.Action.GetLabeled(), ts)
	}

	payload := buildLabelAssociationPayload(ctx, evt, client)
	if payload == nil {
		return
	}
	forwardAppStateEventAsync(ctx, eventTypeLabelAssociation, payload, ts, deviceID)
}

// forwardAppStateEventAsync / forwardAppStateEventToWebhook are shared by
// the label events and the chat.read_state event (event_read_state.go).
func forwardAppStateEventAsync(ctx context.Context, eventName string, payload map[string]any, ts time.Time, deviceID string) {
	go func() {
		webhookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := forwardAppStateEventToWebhook(webhookCtx, eventName, payload, ts, deviceID); err != nil {
			log.Errorf("Failed to forward %s event to webhook: %v", eventName, err)
		}
	}()
}

func forwardAppStateEventToWebhook(ctx context.Context, eventName string, payload map[string]any, ts time.Time, deviceID string) error {
	body := map[string]any{
		"event":     eventName,
		"timestamp": labelEventTimestamp(ts),
		"payload":   payload,
	}
	if deviceID != "" {
		body["device_id"] = deviceID
	}
	return forwardPayloadToConfiguredWebhooks(ctx, body, eventName)
}

// buildLabelEditPayload returns the `label.edit` payload, or nil when the
// event carries nothing forwardable.
func buildLabelEditPayload(evt *events.LabelEdit) map[string]any {
	if evt == nil || evt.LabelID == "" || evt.Action == nil {
		return nil
	}
	payload := map[string]any{"label_id": evt.LabelID}
	addLabelEditActionFields(payload, evt.Action)
	return payload
}

// buildLabelAssociationPayload returns the `label.association` payload, or
// nil when the event carries nothing forwardable.
func buildLabelAssociationPayload(ctx context.Context, evt *events.LabelAssociationChat, client *whatsmeow.Client) map[string]any {
	if evt == nil || evt.LabelID == "" || evt.Action == nil {
		return nil
	}
	payload := map[string]any{
		"label_id": evt.LabelID,
		"labeled":  evt.Action.GetLabeled(),
	}
	addLabelChatFields(ctx, client, payload, evt.JID)
	return payload
}

func addLabelEditActionFields(payload map[string]any, action *waSyncAction.LabelEditAction) {
	if action.Name != nil {
		payload["name"] = action.GetName()
	}
	if action.Color != nil {
		payload["color"] = action.GetColor()
	}
	if action.PredefinedID != nil {
		payload["predefined_id"] = action.GetPredefinedID()
	}
	if action.Deleted != nil {
		payload["deleted"] = action.GetDeleted()
	}
	if action.OrderIndex != nil {
		payload["order_index"] = action.GetOrderIndex()
	}
	if action.IsActive != nil {
		payload["is_active"] = action.GetIsActive()
	}
	if action.Type != nil {
		payload["type"] = action.GetType().String()
	}
	if action.IsImmutable != nil {
		payload["is_immutable"] = action.GetIsImmutable()
	}
	if action.MuteEndTimeMS != nil {
		payload["mute_end_time_ms"] = action.GetMuteEndTimeMS()
	}
}

// addLabelChatFields sets chat_id (phone-number JID when resolvable) and,
// when the index was written with the chat's LID, chat_lid.
func addLabelChatFields(ctx context.Context, client *whatsmeow.Client, payload map[string]any, jid types.JID) {
	chatJID := jid.ToNonAD()
	if chatJID.Server == "lid" {
		payload["chat_lid"] = chatJID.String()
		if client != nil {
			chatJID = NormalizeJIDFromLID(ctx, chatJID, client).ToNonAD()
		}
	}
	payload["chat_id"] = chatJID.String()
}

// labelEventTimestamp formats an already-normalized event time (see
// labelstate.NormalizeEventTime: raw values below 1e12 are seconds, zero is
// "now", so `label.edit` no longer reports 1970).
func labelEventTimestamp(ts time.Time) string {
	if ts.IsZero() || ts.UnixMilli() <= 0 {
		ts = time.Now()
	}
	return ts.UTC().Format(time.RFC3339)
}
