package label

import "context"

// ILabelUsecase is the fork-specific labels API.
type ILabelUsecase interface {
	// ListLabels returns the labels and chat<->label associations of the
	// `regular` app state collection, from the per-client cache or from a
	// full resync (first call, or request.Refresh).
	ListLabels(ctx context.Context, request ListLabelsRequest) (response ListLabelsResponse, err error)
	// CreateLabel writes a new `label_edit` record with id = max known id + 1.
	CreateLabel(ctx context.Context, request CreateLabelRequest) (response CreateLabelResponse, err error)
	// UpdateLabel renames and/or recolors an existing label, resending the
	// full last known record (a SET replaces the whole record).
	UpdateLabel(ctx context.Context, request UpdateLabelRequest) (response UpdateLabelResponse, err error)
	// DeleteLabel marks a label deleted. Its chat associations are not
	// touched (WhatsApp leaves them too); ListLabels filters them out.
	DeleteLabel(ctx context.Context, request DeleteLabelRequest) (response DeleteLabelResponse, err error)
}
