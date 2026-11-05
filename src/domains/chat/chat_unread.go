package chat

// Chat read-state operations (fork-specific; see FORK_CHANGES.md).
//
// POST /chat/{chat_jid}/unread marks a chat as unread (or read) the way the
// phone's "Mark as unread" menu entry does: an app state mutation
// (`markChatAsRead` in the `regular_low` collection), not a read receipt.
// Read receipts (`POST /message/{id}/read`) are a different mechanism that
// tells the *sender* their message was read; this one only moves the chat's
// unread marker on the account's own devices.

const (
	// UnreadJIDFormPN writes the mutation under the phone-number JID.
	UnreadJIDFormPN = "pn"
	// UnreadJIDFormLID writes it under the chat's LID (falls back to the
	// phone number when no LID is known).
	UnreadJIDFormLID = "lid"
	// UnreadJIDFormBoth writes both forms in one patch.
	UnreadJIDFormBoth = "both"
)

type UnreadChatRequest struct {
	ChatJID string `json:"chat_jid" uri:"chat_jid"`
	// Unread is optional and defaults to true. `false` writes read=true via
	// app state (the symmetric operation, "mark as read" from the chat
	// list), which is not the same as sending read receipts.
	Unread *bool `json:"unread"`
	// JIDForm selects the index key form the mutation is written under:
	// "pn" (default), "lid" or "both". See FORK_CHANGES.md for which form
	// the phone honours.
	JIDForm string `json:"jid_form"`
}

type UnreadChatResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	ChatJID string `json:"chat_jid"`
	// ChatLID is set when the mutation was written under the chat's LID
	// (jid_form "lid" or "both" with a known LID).
	ChatLID string `json:"chat_lid,omitempty"`
	Unread  bool   `json:"unread"`
	// LastMessageTimestamp is the RFC3339 time of the newest stored message
	// of the chat (the `messageRange.lastMessageTimestamp` sent), or the
	// request time when the chat has no stored message.
	LastMessageTimestamp string `json:"last_message_timestamp"`
	// LastMessageID is the id of that message (the key sent in the
	// range), omitted when the chat has no stored message.
	LastMessageID string `json:"last_message_id,omitempty"`
}
