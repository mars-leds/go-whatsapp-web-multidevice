package rest

import (
	"context"
	"errors"
	"net/http"
	"strings"

	domainMessage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/message"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
)

// MediaRetryMessage handles POST /message/:message_id/media-retry.
//
// Unlike the other endpoints in this file, this handler translates
// domain errors to explicit HTTP status codes (404 / 502 / 504) instead
// of wrapping everything in a generic 500 via utils.PanicIfNeeded, so
// callers can tell "media is gone" from "timed out waiting".
func (controller *Message) MediaRetryMessage(c fiber.Ctx) error {
	var request domainMessage.MediaRetryRequest

	// Accept phone + timeout_seconds from body for POST semantics, but
	// also honor ?phone=... to stay symmetric with GET /download.
	_ = c.Bind().Body(&request)
	if request.Phone == "" {
		request.Phone = c.Query("phone")
	}
	request.MessageID = c.Params("message_id")
	utils.SanitizePhone(&request.Phone)

	ctx := c.Context()
	if device, ok := c.Locals("device").(*whatsapp.DeviceInstance); ok {
		ctx = whatsapp.ContextWithDevice(ctx, device)
	}

	response, err := controller.Service.MediaRetry(ctx, request)
	if err != nil {
		status := classifyMediaRetryError(err)
		return c.Status(status).JSON(utils.ResponseData{
			Status:  status,
			Code:    "MEDIA_RETRY_FAILED",
			Message: err.Error(),
		})
	}

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: response.Status,
		Results: response,
	})
}

// classifyMediaRetryError maps usecase error strings to HTTP status
// codes. String matching is intentional — the usecase returns plain
// fmt.Errorf values rather than typed errors to stay close to the
// upstream style.
func classifyMediaRetryError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return http.StatusGatewayTimeout
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"),
		strings.Contains(msg, "has no media"),
		strings.Contains(msg, "has no stored mediaKey"),
		strings.Contains(msg, "does not belong to chat"):
		return http.StatusNotFound
	case strings.Contains(msg, "no media retry notification received within"):
		return http.StatusGatewayTimeout
	case strings.Contains(msg, "refused by sender"),
		strings.Contains(msg, "result "),
		strings.Contains(msg, "decrypt media retry"),
		strings.Contains(msg, "download media via new path"),
		strings.Contains(msg, "no new direct path"):
		return http.StatusBadGateway
	case strings.Contains(msg, "is required"),
		strings.Contains(msg, "unsupported media type"),
		strings.Contains(msg, "invalid stored sender"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
