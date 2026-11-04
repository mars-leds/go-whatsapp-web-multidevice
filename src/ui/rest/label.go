package rest

import (
	"strings"

	domainLabel "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/label"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
)

// Label is the fork-specific labels controller (see FORK_CHANGES.md).
type Label struct {
	Service domainLabel.ILabelUsecase
}

func InitRestLabel(app fiber.Router, service domainLabel.ILabelUsecase) Label {
	rest := Label{Service: service}

	app.Get("/labels", rest.ListLabels)
	app.Post("/labels", rest.CreateLabel)
	app.Put("/labels/:label_id", rest.UpdateLabel)
	app.Delete("/labels/:label_id", rest.DeleteLabel)

	return rest
}

// ListLabels handles GET /labels?refresh=true|false.
func (controller *Label) ListLabels(c fiber.Ctx) error {
	request := domainLabel.ListLabelsRequest{
		Refresh: fiber.Query[bool](c, "refresh", false),
	}

	response, err := controller.Service.ListLabels(whatsapp.ContextWithDevice(c.Context(), getDeviceFromCtx(c)), request)
	utils.PanicIfNeeded(err)

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: "Success get labels",
		Results: response,
	})
}

// CreateLabel handles POST /labels {name, color?}.
func (controller *Label) CreateLabel(c fiber.Ctx) error {
	var request domainLabel.CreateLabelRequest
	if err := c.Bind().Body(&request); err != nil {
		return badRequest(c, "Invalid request body")
	}

	response, err := controller.Service.CreateLabel(whatsapp.ContextWithDevice(c.Context(), getDeviceFromCtx(c)), request)
	utils.PanicIfNeeded(err)

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: response.Message,
		Results: response,
	})
}

// UpdateLabel handles PUT /labels/:label_id {name?, color?}. The route id is
// authoritative; a body label_id never overrides it.
func (controller *Label) UpdateLabel(c fiber.Ctx) error {
	var request domainLabel.UpdateLabelRequest
	if err := c.Bind().Body(&request); err != nil {
		return badRequest(c, "Invalid request body")
	}
	request.LabelID = labelIDParam(c)

	response, err := controller.Service.UpdateLabel(whatsapp.ContextWithDevice(c.Context(), getDeviceFromCtx(c)), request)
	utils.PanicIfNeeded(err)

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: response.Message,
		Results: response,
	})
}

// DeleteLabel handles DELETE /labels/:label_id (no body).
func (controller *Label) DeleteLabel(c fiber.Ctx) error {
	request := domainLabel.DeleteLabelRequest{LabelID: labelIDParam(c)}

	response, err := controller.Service.DeleteLabel(whatsapp.ContextWithDevice(c.Context(), getDeviceFromCtx(c)), request)
	utils.PanicIfNeeded(err)

	return c.JSON(utils.ResponseData{
		Status:  200,
		Code:    "SUCCESS",
		Message: response.Message,
		Results: response,
	})
}

// labelIDParam returns the label_id path parameter detached from fiber's
// reusable buffer.
func labelIDParam(c fiber.Ctx) string {
	return strings.Clone(c.Params("label_id"))
}

func badRequest(c fiber.Ctx, message string) error {
	return c.Status(400).JSON(utils.ResponseData{
		Status:  400,
		Code:    "BAD_REQUEST",
		Message: message,
		Results: nil,
	})
}
