package whatsapp

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/config"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chatstorage"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

func mustJID(t *testing.T, raw string) types.JID {
	t.Helper()
	jid, err := types.ParseJID(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return jid
}

// The payloads below are exactly what upstream built from the raw
// *events.AppState; the Worker consumes them, so they must not drift.
func TestBuildLabelEditPayload(t *testing.T) {
	tests := []struct {
		name    string
		evt     *events.LabelEdit
		payload map[string]any
	}{
		{
			name: "label edit",
			evt: &events.LabelEdit{
				LabelID: "9",
				Action: &waSyncAction.LabelEditAction{
					Name:        proto.String("Accounts"),
					Color:       proto.Int32(8),
					Deleted:     proto.Bool(false),
					OrderIndex:  proto.Int32(9),
					IsActive:    proto.Bool(true),
					IsImmutable: proto.Bool(false),
					Type:        waSyncAction.LabelEditAction_CUSTOM.Enum(),
				},
			},
			payload: map[string]any{
				"label_id":     "9",
				"name":         "Accounts",
				"color":        int32(8),
				"deleted":      false,
				"order_index":  int32(9),
				"is_active":    true,
				"is_immutable": false,
				"type":         "CUSTOM",
			},
		},
		{
			name: "only the fields WhatsApp sent",
			evt: &events.LabelEdit{
				LabelID: "9",
				Action:  &waSyncAction.LabelEditAction{Name: proto.String("Test"), PredefinedID: proto.Int32(1), MuteEndTimeMS: proto.Int64(0)},
			},
			payload: map[string]any{"label_id": "9", "name": "Test", "predefined_id": int32(1), "mute_end_time_ms": int64(0)},
		},
		{name: "nil evt", evt: nil},
		{name: "empty label id", evt: &events.LabelEdit{LabelID: "", Action: &waSyncAction.LabelEditAction{Name: proto.String("Test")}}},
		{name: "nil action", evt: &events.LabelEdit{LabelID: "9", Action: nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPayloadEqual(t, buildLabelEditPayload(tt.evt), tt.payload)
		})
	}
}

func TestBuildLabelAssociationPayload(t *testing.T) {
	tests := []struct {
		name    string
		evt     *events.LabelAssociationChat
		payload map[string]any
	}{
		{
			name: "group chat",
			evt: &events.LabelAssociationChat{
				JID:     mustJID(t, "120363424051089958@g.us"),
				LabelID: "9",
				Action:  &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)},
			},
			payload: map[string]any{"label_id": "9", "labeled": true, "chat_id": "120363424051089958@g.us"},
		},
		{
			name: "lid without a client keeps the lid as chat_id",
			evt: &events.LabelAssociationChat{
				JID:     mustJID(t, "223754944819424@lid"),
				LabelID: "9",
				Action:  &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(false)},
			},
			payload: map[string]any{"label_id": "9", "labeled": false, "chat_id": "223754944819424@lid", "chat_lid": "223754944819424@lid"},
		},
		{
			name: "device suffix is dropped",
			evt: &events.LabelAssociationChat{
				JID:     mustJID(t, "6289685028129:12@s.whatsapp.net"),
				LabelID: "9",
				Action:  &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)},
			},
			payload: map[string]any{"label_id": "9", "labeled": true, "chat_id": "6289685028129@s.whatsapp.net"},
		},
		{name: "nil evt", evt: nil},
		{name: "nil action", evt: &events.LabelAssociationChat{JID: mustJID(t, "120363424051089958@g.us"), LabelID: "9"}},
		{name: "empty label id", evt: &events.LabelAssociationChat{JID: mustJID(t, "120363424051089958@g.us"), Action: &waSyncAction.LabelAssociationAction{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPayloadEqual(t, buildLabelAssociationPayload(context.Background(), tt.evt, nil), tt.payload)
		})
	}
}

func TestLabelEventTimestamp(t *testing.T) {
	t.Run("zero falls back to now", func(t *testing.T) {
		before := time.Now().UTC().Truncate(time.Second)
		parsed, err := time.Parse(time.RFC3339, labelEventTimestamp(time.Time{}))
		if err != nil {
			t.Fatalf("expected RFC3339: %v", err)
		}
		if parsed.Before(before) {
			t.Fatalf("expected %s >= %s", parsed, before)
		}
	})

	t.Run("phone seconds are not 1970 anymore", func(t *testing.T) {
		// whatsmeow builds evt.Timestamp = time.UnixMilli(raw); the phone's
		// raw value for label_edit is in seconds.
		got := labelEventTimestamp(labelstate.NormalizeEventTime(time.UnixMilli(1_769_040_180)))
		if got != "2026-01-22T00:03:00Z" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("milliseconds pass through", func(t *testing.T) {
		got := labelEventTimestamp(labelstate.NormalizeEventTime(time.UnixMilli(1_769_040_180_000)))
		if got != "2026-01-22T00:03:00Z" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLabelHandlersSkipFullSyncButApplyIncremental(t *testing.T) {
	// Webhook delivery is exercised through the configured-webhooks path with
	// no targets, so nothing is sent; what matters here is the cache.
	ensureTestLogger()
	originalWebhooks := config.WhatsappWebhook
	config.WhatsappWebhook = nil
	defer func() { config.WhatsappWebhook = originalWebhooks }()
	client := newLabelSyncTestClient(t)
	t.Cleanup(func() { labelstate.Forget(client) })
	state := LabelStateForClient(client)

	full := &events.LabelEdit{LabelID: "1", Action: &waSyncAction.LabelEditAction{Name: proto.String("replay")}, FromFullSync: true}
	handleLabelEdit(context.Background(), full, "dev", client)
	if len(state.Labels()) != 0 {
		t.Fatal("full-sync replays must not touch the cache from the main handler")
	}

	live := &events.LabelEdit{LabelID: "2", Timestamp: time.UnixMilli(1_700_000_000), Action: &waSyncAction.LabelEditAction{Name: proto.String("live")}}
	handleLabelEdit(context.Background(), live, "dev", client)
	l, ok := state.Label("2")
	if !ok || l.Action.GetName() != "live" {
		t.Fatalf("expected live label in cache, got %+v (%v)", l, ok)
	}
	if !l.Timestamp.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("expected normalized timestamp, got %s", l.Timestamp)
	}

	assocFull := &events.LabelAssociationChat{JID: mustJID(t, "111@lid"), LabelID: "2", Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)}, FromFullSync: true}
	handleLabelAssociation(context.Background(), assocFull, "dev", client)
	if len(state.Associations()) != 0 {
		t.Fatal("full-sync association replays must not touch the cache")
	}
	assocLive := &events.LabelAssociationChat{JID: mustJID(t, "111@lid"), LabelID: "2", Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)}}
	handleLabelAssociation(context.Background(), assocLive, "dev", client)
	assocs := state.Associations()
	if len(assocs) != 1 || assocs[0].RawJID != "111@lid" || !assocs[0].Labeled {
		t.Fatalf("expected the live association in cache, got %+v", assocs)
	}

	// Nil-safe.
	handleLabelEdit(context.Background(), nil, "dev", nil)
	handleLabelAssociation(context.Background(), nil, "dev", nil)
	handleLabelEdit(context.Background(), live, "dev", nil)
}

func assertPayloadEqual(t *testing.T, got, want map[string]any) {
	t.Helper()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// ensureTestLogger initializes the package logger, which InitWaDB normally
// sets up and unit tests never run.
func ensureTestLogger() {
	if log == nil {
		log = waLog.Noop
	}
}

// TestHandleLabelEdit_DeviceWebhookOnly_ForwardsEvent verifies that label
// events are forwarded when only a device-specific webhook is configured and
// no global webhook is set (regression test ported from upstream's
// handleAppState version), that the body carries the fixed timestamp, and
// that a full-sync replay of the same event is NOT forwarded.
func TestHandleLabelEdit_DeviceWebhookOnly_ForwardsEvent(t *testing.T) {
	ensureTestLogger()

	originalWebhooks := config.WhatsappWebhook
	config.WhatsappWebhook = nil
	defer func() { config.WhatsappWebhook = originalWebhooks }()

	deviceWebhookURL := "https://device-only-webhook.example.com"
	originalStorageForTest := webhookStorageForTest
	webhookStorageForTest = func(deviceJID string) (*chatstorage.DeviceRecord, error) {
		return &chatstorage.DeviceRecord{
			DeviceID:   deviceJID,
			WebhookURL: &deviceWebhookURL,
		}, nil
	}
	defer func() { webhookStorageForTest = originalStorageForTest }()

	type delivery struct {
		url  string
		body map[string]any
	}
	called := make(chan delivery, 2)
	originalSubmit := submitWebhookFn
	submitWebhookFn = func(_ context.Context, body map[string]any, url string, _ *chatstorage.DeviceWebhookConfig) error {
		called <- delivery{url: url, body: body}
		return nil
	}
	defer func() { submitWebhookFn = originalSubmit }()

	evt := &events.LabelEdit{
		LabelID:   "9",
		Timestamp: time.UnixMilli(1_769_040_180), // phone-style seconds
		Action:    &waSyncAction.LabelEditAction{Name: proto.String("Accounts")},
	}
	handleLabelEdit(context.Background(), evt, "device_1", nil)

	select {
	case d := <-called:
		if d.url != deviceWebhookURL {
			t.Fatalf("expected forward to device webhook %s, got %s", deviceWebhookURL, d.url)
		}
		if d.body["event"] != eventTypeLabelEdit || d.body["device_id"] != "device_1" {
			t.Fatalf("unexpected envelope: %#v", d.body)
		}
		if d.body["timestamp"] != "2026-01-22T00:03:00Z" {
			t.Fatalf("expected normalized timestamp, got %#v", d.body["timestamp"])
		}
		assertPayloadEqual(t, d.body["payload"].(map[string]any), map[string]any{"label_id": "9", "name": "Accounts"})
	case <-time.After(2 * time.Second):
		t.Fatal("label event was not forwarded when only a device webhook is configured")
	}

	replay := *evt
	replay.FromFullSync = true
	handleLabelEdit(context.Background(), &replay, "device_1", nil)
	select {
	case d := <-called:
		t.Fatalf("full-sync replay must not be forwarded, got %#v", d.body)
	case <-time.After(300 * time.Millisecond):
	}
}
