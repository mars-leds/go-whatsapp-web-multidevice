package validations

import (
	"context"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// ValidateLabelChat validates the fork-specific POST /chat/{chat_jid}/label
// request. `labeled` is validated with NotNil (not Required) because `false`
// is a legitimate value meaning "remove the label".
func ValidateLabelChat(ctx context.Context, request *domainChat.LabelChatRequest) error {
	err := validation.ValidateStructWithContext(ctx, request,
		validation.Field(&request.ChatJID, validation.Required),
		validation.Field(&request.LabelID, validation.Required),
		validation.Field(&request.LabelName, validation.Required),
		validation.Field(&request.Labeled, validation.NotNil),
	)

	if err != nil {
		return pkgError.ValidationError(err.Error())
	}

	return nil
}
