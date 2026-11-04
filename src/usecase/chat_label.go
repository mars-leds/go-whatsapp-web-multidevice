package usecase

import (
	"context"
	"fmt"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/validations"
	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow/appstate"
)

// LabelChat applies or removes a WhatsApp (Business) label on a chat.
//
// Fork-specific. It works exactly like PinChat / ArchiveChat: it builds an
// app state patch with whatsmeow's appstate.BuildLabelChat (collection
// `regular`, index `label_jid`) and pushes it with SendAppState. The phone
// then propagates the association to every linked device and whatsmeow
// emits the matching *events.AppState, which the existing label webhook
// forwarder turns into a `label.association` webhook event.
//
// The label must already exist on the phone: this endpoint only creates
// the chat<->label association, it never creates or renames labels
// (that would be appstate.BuildLabelEdit, intentionally not exposed here).
func (service serviceChat) LabelChat(ctx context.Context, request domainChat.LabelChatRequest) (response domainChat.LabelChatResponse, err error) {
	if err = validations.ValidateLabelChat(ctx, &request); err != nil {
		return response, err
	}
	labeled := *request.Labeled

	client := whatsapp.ClientFromContext(ctx)
	if client == nil {
		return response, pkgError.ErrWaCLI
	}

	// Validate JID and ensure connection
	targetJID, err := utils.ValidateJidWithLogin(client, request.ChatJID)
	if err != nil {
		return response, err
	}

	// Build label association patch using whatsmeow's BuildLabelChat
	patchInfo := appstate.BuildLabelChat(targetJID, request.LabelID, labeled)

	// Send app state update
	if err = client.SendAppState(ctx, patchInfo); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"chat_jid":   request.ChatJID,
			"label_id":   request.LabelID,
			"label_name": request.LabelName,
			"labeled":    labeled,
		}).Error("Failed to send label chat app state")
		return response, err
	}

	// Build response
	response.Status = "success"
	response.ChatJID = request.ChatJID
	response.LabelID = request.LabelID
	response.LabelName = request.LabelName
	response.Labeled = labeled
	response.Message = labelChatMessage(request.LabelName, labeled)

	logrus.WithFields(logrus.Fields{
		"chat_jid":   request.ChatJID,
		"label_id":   request.LabelID,
		"label_name": request.LabelName,
		"labeled":    labeled,
	}).Info("Chat label operation completed successfully")

	return response, nil
}

func labelChatMessage(labelName string, labeled bool) string {
	if labeled {
		return fmt.Sprintf("Chat labeled successfully with label '%s'", labelName)
	}
	return fmt.Sprintf("Label '%s' removed from chat successfully", labelName)
}
