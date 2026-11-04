package chat

// Label Chat operations (fork-specific; see FORK_CHANGES.md).
//
// The contract mirrors the `labelChat` operation that upstream documented in
// docs/openapi.yaml between v6.8.0 and v8.10.0 but never implemented:
// POST /chat/{chat_jid}/label with {label_id, label_name, labeled}.

type LabelChatRequest struct {
	ChatJID string `json:"chat_jid" uri:"chat_jid"`
	// LabelID is the WhatsApp label identifier (the `label_id` reported by
	// the `label.edit` / `label.association` webhook events).
	LabelID string `json:"label_id"`
	// LabelName is informational only: it is echoed back in the response
	// and logs. The label itself must already exist on the phone.
	LabelName string `json:"label_name"`
	// Labeled is a pointer so that a missing field is rejected instead of
	// being silently treated as "remove the label".
	Labeled *bool `json:"labeled"`
}

type LabelChatResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	ChatJID string `json:"chat_jid"`
	// ChatLID is the chat's LID when whatsmeow knows it; it is then the
	// index form the association was written with (the phone's form).
	ChatLID   string `json:"chat_lid,omitempty"`
	LabelID   string `json:"label_id"`
	LabelName string `json:"label_name"`
	Labeled   bool   `json:"labeled"`
}
