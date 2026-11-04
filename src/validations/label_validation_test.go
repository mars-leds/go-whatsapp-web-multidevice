package validations

import (
	"context"
	"testing"

	domainLabel "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/label"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/stretchr/testify/assert"
)

func int32Ptr(v int32) *int32 { return &v }
func strPtr(v string) *string { return &v }

func TestValidateCreateLabel(t *testing.T) {
	tests := []struct {
		name    string
		request domainLabel.CreateLabelRequest
		err     any
	}{
		{name: "name and color", request: domainLabel.CreateLabelRequest{Name: "IRRITADO", Color: int32Ptr(7)}, err: nil},
		{name: "color omitted", request: domainLabel.CreateLabelRequest{Name: "IRRITADO"}, err: nil},
		{name: "color zero", request: domainLabel.CreateLabelRequest{Name: "IRRITADO", Color: int32Ptr(0)}, err: nil},
		{name: "color max", request: domainLabel.CreateLabelRequest{Name: "IRRITADO", Color: int32Ptr(19)}, err: nil},
		{name: "empty name", request: domainLabel.CreateLabelRequest{Name: ""}, err: pkgError.ValidationError("name: cannot be blank.")},
		{name: "color too high", request: domainLabel.CreateLabelRequest{Name: "X", Color: int32Ptr(20)}, err: pkgError.ValidationError("color: must be no greater than 19.")},
		{name: "color negative", request: domainLabel.CreateLabelRequest{Name: "X", Color: int32Ptr(-1)}, err: pkgError.ValidationError("color: must be no less than 0.")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCreateLabel(context.Background(), &tt.request)
			assert.Equal(t, tt.err, err)
		})
	}
}

func TestValidateUpdateLabel(t *testing.T) {
	tests := []struct {
		name    string
		request domainLabel.UpdateLabelRequest
		err     any
	}{
		{name: "rename", request: domainLabel.UpdateLabelRequest{LabelID: "13", Name: strPtr("ATENCION HUMANA")}, err: nil},
		{name: "recolor", request: domainLabel.UpdateLabelRequest{LabelID: "13", Color: int32Ptr(3)}, err: nil},
		{name: "both", request: domainLabel.UpdateLabelRequest{LabelID: "13", Name: strPtr("X"), Color: int32Ptr(3)}, err: nil},
		{name: "missing id", request: domainLabel.UpdateLabelRequest{Name: strPtr("X")}, err: pkgError.ValidationError("label_id: cannot be blank.")},
		{name: "nothing to change", request: domainLabel.UpdateLabelRequest{LabelID: "13"}, err: pkgError.ValidationError("at least one of name or color is required.")},
		{name: "empty name", request: domainLabel.UpdateLabelRequest{LabelID: "13", Name: strPtr("")}, err: pkgError.ValidationError("name: cannot be blank.")},
		{name: "color too high", request: domainLabel.UpdateLabelRequest{LabelID: "13", Color: int32Ptr(99)}, err: pkgError.ValidationError("color: must be no greater than 19.")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUpdateLabel(context.Background(), &tt.request)
			assert.Equal(t, tt.err, err)
		})
	}
}

func TestValidateDeleteLabel(t *testing.T) {
	assert.Nil(t, ValidateDeleteLabel(context.Background(), &domainLabel.DeleteLabelRequest{LabelID: "13"}))
	assert.Equal(t,
		pkgError.ValidationError("label_id: cannot be blank."),
		ValidateDeleteLabel(context.Background(), &domainLabel.DeleteLabelRequest{}),
	)
}
