package whatsapp

import (
	"context"
	"testing"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/config"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chatstorage"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestBuildChatReadStatePayload(t *testing.T) {
	ensureTestLogger()
	tests := []struct {
		name    string
		evt     *events.MarkChatAsRead
		payload map[string]any
	}{
		{
			name: "unread with range and key",
			evt: &events.MarkChatAsRead{
				JID: mustJID(t, "6289685028129@s.whatsapp.net"),
				Action: &waSyncAction.MarkChatAsReadAction{
					Read: proto.Bool(false),
					MessageRange: &waSyncAction.SyncActionMessageRange{
						LastMessageTimestamp: proto.Int64(1_700_000_000),
						Messages: []*waSyncAction.SyncActionMessage{{
							Key:       &waCommon.MessageKey{ID: proto.String("ABC"), FromMe: proto.Bool(true)},
							Timestamp: proto.Int64(1_700_000_000),
						}},
					},
				},
			},
			payload: map[string]any{
				"chat_id":                "6289685028129@s.whatsapp.net",
				"read":                   false,
				"last_message_timestamp": "2023-11-14T22:13:20Z",
				"last_message_id":        "ABC",
			},
		},
		{
			name: "read, no range",
			evt: &events.MarkChatAsRead{
				JID:    mustJID(t, "120363402106@g.us"),
				Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(true)},
			},
			payload: map[string]any{"chat_id": "120363402106@g.us", "read": true},
		},
		{
			name: "LID index keeps chat_lid (unresolvable without a client)",
			evt: &events.MarkChatAsRead{
				JID:    mustJID(t, "223754944819424@lid"),
				Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false), MessageRange: &waSyncAction.SyncActionMessageRange{}},
			},
			payload: map[string]any{"chat_id": "223754944819424@lid", "chat_lid": "223754944819424@lid", "read": false},
		},
		{name: "nil action", evt: &events.MarkChatAsRead{JID: mustJID(t, "1@s.whatsapp.net")}, payload: nil},
		{name: "empty jid", evt: &events.MarkChatAsRead{Action: &waSyncAction.MarkChatAsReadAction{}}, payload: nil},
		{name: "nil event", evt: nil, payload: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPayloadEqual(t, buildChatReadStatePayload(context.Background(), tt.evt, nil), tt.payload)
		})
	}
}

func TestHandleMarkChatAsReadForwardsIncrementalOnly(t *testing.T) {
	ensureTestLogger()

	originalWebhooks := config.WhatsappWebhook
	config.WhatsappWebhook = nil
	defer func() { config.WhatsappWebhook = originalWebhooks }()

	deviceWebhookURL := "https://device-only-webhook.example.com"
	originalStorageForTest := webhookStorageForTest
	webhookStorageForTest = func(deviceJID string) (*chatstorage.DeviceRecord, error) {
		return &chatstorage.DeviceRecord{DeviceID: deviceJID, WebhookURL: &deviceWebhookURL}, nil
	}
	defer func() { webhookStorageForTest = originalStorageForTest }()

	called := make(chan map[string]any, 2)
	originalSubmit := submitWebhookFn
	submitWebhookFn = func(_ context.Context, body map[string]any, _ string, _ *chatstorage.DeviceWebhookConfig) error {
		called <- body
		return nil
	}
	defer func() { submitWebhookFn = originalSubmit }()

	evt := &events.MarkChatAsRead{
		JID:       mustJID(t, "6289685028129@s.whatsapp.net"),
		Timestamp: time.UnixMilli(1_769_040_180_000),
		Action:    &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false)},
	}
	handleMarkChatAsRead(context.Background(), evt, "device_1", nil)

	select {
	case body := <-called:
		if body["event"] != eventTypeChatReadState {
			t.Fatalf("expected event %q, got %v", eventTypeChatReadState, body["event"])
		}
		if body["device_id"] != "device_1" {
			t.Fatalf("expected device_id device_1, got %v", body["device_id"])
		}
		if body["timestamp"] != "2026-01-22T00:03:00Z" {
			t.Fatalf("unexpected timestamp %v", body["timestamp"])
		}
		payload, _ := body["payload"].(map[string]any)
		assertPayloadEqual(t, payload, map[string]any{"chat_id": "6289685028129@s.whatsapp.net", "read": false})
	case <-time.After(2 * time.Second):
		t.Fatal("chat.read_state was not forwarded")
	}

	replay := *evt
	replay.FromFullSync = true
	handleMarkChatAsRead(context.Background(), &replay, "device_1", nil)
	handleMarkChatAsRead(context.Background(), nil, "device_1", nil)
	select {
	case body := <-called:
		t.Fatalf("full-sync replay must not be forwarded, got %v", body)
	case <-time.After(300 * time.Millisecond):
	}
}
