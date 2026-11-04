package validations

import (
	"context"

	domainLabel "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/label"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// WhatsApp Business offers a fixed palette of 20 label colors (0-19).
const (
	labelColorMin int32 = 0
	labelColorMax int32 = 19
	labelNameMax        = 100
)

// Fork-specific validations of the labels API (see FORK_CHANGES.md).

func ValidateCreateLabel(ctx context.Context, request *domainLabel.CreateLabelRequest) error {
	err := validation.ValidateStructWithContext(ctx, request,
		validation.Field(&request.Name, validation.Required, validation.Length(1, labelNameMax)),
		validation.Field(&request.Color, validation.Min(labelColorMin), validation.Max(labelColorMax)),
	)
	if err != nil {
		return pkgError.ValidationError(err.Error())
	}
	return nil
}

func ValidateUpdateLabel(ctx context.Context, request *domainLabel.UpdateLabelRequest) error {
	err := validation.ValidateStructWithContext(ctx, request,
		validation.Field(&request.LabelID, validation.Required),
		validation.Field(&request.Name, validation.NilOrNotEmpty, validation.Length(1, labelNameMax)),
		validation.Field(&request.Color, validation.Min(labelColorMin), validation.Max(labelColorMax)),
	)
	if err != nil {
		return pkgError.ValidationError(err.Error())
	}
	if request.Name == nil && request.Color == nil {
		return pkgError.ValidationError("at least one of name or color is required.")
	}
	return nil
}

func ValidateDeleteLabel(ctx context.Context, request *domainLabel.DeleteLabelRequest) error {
	err := validation.ValidateStructWithContext(ctx, request,
		validation.Field(&request.LabelID, validation.Required),
	)
	if err != nil {
		return pkgError.ValidationError(err.Error())
	}
	return nil
}
