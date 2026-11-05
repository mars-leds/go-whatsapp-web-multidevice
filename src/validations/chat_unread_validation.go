package validations

import (
	"context"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// ValidateUnreadChat validates the fork-specific POST /chat/{chat_jid}/unread
// request. `unread` is optional (nil means true); `jid_form` must be empty
// or one of pn / lid / both.
func ValidateUnreadChat(ctx context.Context, request *domainChat.UnreadChatRequest) error {
	err := validation.ValidateStructWithContext(ctx, request,
		validation.Field(&request.ChatJID, validation.Required),
		validation.Field(&request.JIDForm, validation.In(
			domainChat.UnreadJIDFormPN,
			domainChat.UnreadJIDFormLID,
			domainChat.UnreadJIDFormBoth,
		)),
	)

	if err != nil {
		return pkgError.ValidationError(err.Error())
	}

	return nil
}
