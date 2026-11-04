package rest

import (
	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
)

// LabelChat handles POST /chat/:chat_jid/label (fork-specific).
//
// Body: {"label_id": "...", "label_name": "...", "labeled": true|false}.
// The route chat_jid is authoritative; a body chat_jid never overrides it.
func (controller *Chat) LabelChat(c fiber.Ctx) error {
	var request domainChat.LabelChatRequest

	// Parse path parameter
	chatJID, err := chatJIDParam(c)
	if err != nil {
		return c.Status(400).JSON(utils.ResponseData{
			Status:  400,
			Code:    "BAD_REQUEST",
			Message: "invalid chat_jid path parameter: " + err.Error(),
			Results: nil,
		})
	}

	// Parse JSON body
	if err := c.Bind().Body(&request); err != nil {
		return c.Status(400).JSON(utils.ResponseData{
			Status:  400,
			Code:    "BAD_REQUEST",
			Message: "Invalid request body",
			Results: nil,
		})
	}
	request.ChatJID = chatJID

	response, err := controller.Service.LabelChat(whatsapp.ContextWithDevice(c.Context(), getDeviceFromCtx(c)), request)
	utils.PanicIfNeeded(err)

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: response.Message,
		Results: response,
	})
}
