// Package label holds the DTOs of the fork-specific WhatsApp (Business)
// labels API: GET/POST /labels, PUT/DELETE /labels/{label_id}.
// See FORK_CHANGES.md.
package label

// Label is a WhatsApp label as last seen in the `regular` app state
// collection (`label_edit` index). Field names match the `label.edit`
// webhook payload.
type Label struct {
	LabelID string `json:"label_id"`
	Name    string `json:"name"`
	Color   int32  `json:"color"`
	Deleted bool   `json:"deleted"`
	// IsActive, Type, PredefinedID and OrderIndex are the extra fields the
	// phone writes on its own labels. They are echoed so a client can tell
	// predefined/system labels from custom ones.
	IsActive     bool   `json:"is_active"`
	Type         string `json:"type"`
	PredefinedID int32  `json:"predefined_id"`
	OrderIndex   int32  `json:"order_index"`
}

// ChatLabel is one chat<->label association after LID->PN normalization
// and dedup. Field names match the `label.association` webhook payload.
type ChatLabel struct {
	ChatID string `json:"chat_id"`
	// ChatLID is set when the phone stored the association under the
	// chat's LID (its usual form); ChatID then carries the resolved phone
	// number JID, or the LID itself if it could not be resolved.
	ChatLID string `json:"chat_lid,omitempty"`
	LabelID string `json:"label_id"`
	Labeled bool   `json:"labeled"`
}

type ListLabelsRequest struct {
	// Refresh forces a full resync of the `regular` collection from the
	// server even if the cache is already populated.
	Refresh bool `json:"refresh" query:"refresh"`
}

type ListLabelsResponse struct {
	Labels []Label     `json:"labels"`
	Chats  []ChatLabel `json:"chats"`
	// Refreshed is true when this call ran a full sync (first call after
	// start/login, or refresh=true).
	Refreshed bool `json:"refreshed"`
}

type CreateLabelRequest struct {
	Name string `json:"name"`
	// Color is the WhatsApp Business palette index (0-19). Defaults to 0.
	Color *int32 `json:"color"`
}

type CreateLabelResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Label   Label  `json:"label"`
}

type UpdateLabelRequest struct {
	LabelID string `json:"label_id" uri:"label_id"`
	// At least one of Name / Color must be set.
	Name  *string `json:"name"`
	Color *int32  `json:"color"`
}

type UpdateLabelResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Label   Label  `json:"label"`
}

type DeleteLabelRequest struct {
	LabelID string `json:"label_id" uri:"label_id"`
}

type DeleteLabelResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Label   Label  `json:"label"`
}
