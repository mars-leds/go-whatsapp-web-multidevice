package rest

import (
	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
)

// UnreadChat handles POST /chat/:chat_jid/unread (fork-specific).
//
// Body (optional): {"unread": true|false, "jid_form": "pn"|"lid"|"both",
// "resync": true|false, "recover": true|false}. An empty body means
// unread=true. `?resync=true` / `?recover=true` in the query are the same
// as the body flags. The route chat_jid is authoritative; a body chat_jid
// never overrides it.
func (controller *Chat) UnreadChat(c fiber.Ctx) error {
	var request domainChat.UnreadChatRequest

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

	// Parse JSON body (optional; see RequestChatHistory for why binding is
	// skipped on an empty body).
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&request); err != nil {
			return c.Status(400).JSON(utils.ResponseData{
				Status:  400,
				Code:    "BAD_REQUEST",
				Message: "Invalid request body",
				Results: nil,
			})
		}
	}
	request.ChatJID = chatJID
	if c.Query("resync") == "true" {
		request.Resync = true
	}
	if c.Query("recover") == "true" {
		request.Recover = true
	}

	response, err := controller.Service.UnreadChat(whatsapp.ContextWithDevice(c.Context(), getDeviceFromCtx(c)), request)
	utils.PanicIfNeeded(err)

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: response.Message,
		Results: response,
	})
}
