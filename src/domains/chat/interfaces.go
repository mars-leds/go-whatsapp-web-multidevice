package chat

import (
	"context"
)

// IChatUsecase defines the interface for chat-related operations
type IChatUsecase interface {
	ListChats(ctx context.Context, request ListChatsRequest) (response ListChatsResponse, err error)
	GetChatMessages(ctx context.Context, request GetChatMessagesRequest) (response GetChatMessagesResponse, err error)
	PinChat(ctx context.Context, request PinChatRequest) (response PinChatResponse, err error)
	SetDisappearingTimer(ctx context.Context, request SetDisappearingTimerRequest) (response SetDisappearingTimerResponse, err error)
	ArchiveChat(ctx context.Context, request ArchiveChatRequest) (response ArchiveChatResponse, err error)
	// RequestChatHistory asks the phone (via an on-demand history sync request)
	// for older messages of a chat, anchored at the oldest message currently
	// stored for that chat.
	RequestChatHistory(ctx context.Context, request RequestChatHistoryRequest) (response RequestChatHistoryResponse, err error)
	// LabelChat applies or removes a WhatsApp label on a chat via an app
	// state patch (appstate.BuildLabelChat). Fork-specific.
	LabelChat(ctx context.Context, request LabelChatRequest) (response LabelChatResponse, err error)
	// UnreadChat marks a chat as unread/read on the account's own devices via
	// an app state patch (appstate.BuildMarkChatAsRead). Fork-specific.
	UnreadChat(ctx context.Context, request UnreadChatRequest) (response UnreadChatResponse, err error)
}
