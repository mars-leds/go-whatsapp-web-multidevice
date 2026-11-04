package validations

import (
	"context"
	"testing"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/stretchr/testify/assert"
)

func TestValidateLabelChat(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	type args struct {
		request domainChat.LabelChatRequest
	}
	tests := []struct {
		name string
		args args
		err  any
	}{
		{
			name: "should success with label request",
			args: args{request: domainChat.LabelChatRequest{
				ChatJID:   "6289685028129@s.whatsapp.net",
				LabelID:   "1",
				LabelName: "Important",
				Labeled:   boolPtr(true),
			}},
			err: nil,
		},
		{
			name: "should success with unlabel request (labeled=false)",
			args: args{request: domainChat.LabelChatRequest{
				ChatJID:   "6289685028129@s.whatsapp.net",
				LabelID:   "1",
				LabelName: "Important",
				Labeled:   boolPtr(false),
			}},
			err: nil,
		},
		{
			name: "should error with empty chat_jid",
			args: args{request: domainChat.LabelChatRequest{
				ChatJID:   "",
				LabelID:   "1",
				LabelName: "Important",
				Labeled:   boolPtr(true),
			}},
			err: pkgError.ValidationError("chat_jid: cannot be blank."),
		},
		{
			name: "should error with empty label_id",
			args: args{request: domainChat.LabelChatRequest{
				ChatJID:   "6289685028129@s.whatsapp.net",
				LabelID:   "",
				LabelName: "Important",
				Labeled:   boolPtr(true),
			}},
			err: pkgError.ValidationError("label_id: cannot be blank."),
		},
		{
			name: "should error with empty label_name",
			args: args{request: domainChat.LabelChatRequest{
				ChatJID:   "6289685028129@s.whatsapp.net",
				LabelID:   "1",
				LabelName: "",
				Labeled:   boolPtr(true),
			}},
			err: pkgError.ValidationError("label_name: cannot be blank."),
		},
		{
			name: "should error with missing labeled",
			args: args{request: domainChat.LabelChatRequest{
				ChatJID:   "6289685028129@s.whatsapp.net",
				LabelID:   "1",
				LabelName: "Important",
				Labeled:   nil,
			}},
			err: pkgError.ValidationError("labeled: is required."),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLabelChat(context.Background(), &tt.args.request)
			assert.Equal(t, tt.err, err)
		})
	}
}
