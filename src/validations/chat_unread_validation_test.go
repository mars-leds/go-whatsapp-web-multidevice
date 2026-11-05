package validations

import (
	"context"
	"testing"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/stretchr/testify/assert"
)

func TestValidateUnreadChat(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	tests := []struct {
		name    string
		request domainChat.UnreadChatRequest
		err     any
	}{
		{
			name:    "default body (unread nil, no jid_form)",
			request: domainChat.UnreadChatRequest{ChatJID: "6289685028129@s.whatsapp.net"},
			err:     nil,
		},
		{
			name:    "explicit read",
			request: domainChat.UnreadChatRequest{ChatJID: "6289685028129@s.whatsapp.net", Unread: boolPtr(false)},
			err:     nil,
		},
		{
			name:    "jid_form lid",
			request: domainChat.UnreadChatRequest{ChatJID: "6289685028129@s.whatsapp.net", JIDForm: "lid"},
			err:     nil,
		},
		{
			name:    "jid_form both",
			request: domainChat.UnreadChatRequest{ChatJID: "6289685028129@s.whatsapp.net", JIDForm: "both"},
			err:     nil,
		},
		{
			name:    "missing chat_jid",
			request: domainChat.UnreadChatRequest{},
			err:     pkgError.ValidationError("chat_jid: cannot be blank."),
		},
		{
			name:    "unknown jid_form",
			request: domainChat.UnreadChatRequest{ChatJID: "6289685028129@s.whatsapp.net", JIDForm: "phone"},
			err:     pkgError.ValidationError("jid_form: must be a valid value."),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUnreadChat(context.Background(), &tt.request)
			assert.Equal(t, tt.err, err)
		})
	}
}
