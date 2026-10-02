package message

import (
	"context"
)

// IMessageActions handles message action operations
type IMessageActions interface {
	MarkAsRead(ctx context.Context, request MarkAsReadRequest) (response GenericResponse, err error)
	MarkAsPlayed(ctx context.Context, request MarkAsPlayedRequest) (response GenericResponse, err error)
	ReactMessage(ctx context.Context, request ReactionRequest) (response GenericResponse, err error)
	RevokeMessage(ctx context.Context, request RevokeRequest) (response GenericResponse, err error)
	UpdateMessage(ctx context.Context, request UpdateMessageRequest) (response GenericResponse, err error)
}

// IMessageManagement handles message management operations
type IMessageManagement interface {
	DeleteMessage(ctx context.Context, request DeleteRequest) (err error)
	StarMessage(ctx context.Context, request StarRequest) (err error)
	DownloadMedia(ctx context.Context, request DownloadMediaRequest) (response DownloadMediaResponse, err error)
	// MediaRetry asks the original sender to re-upload media whose CDN URL
	// has expired and downloads it via the new DirectPath. Fork-specific.
	MediaRetry(ctx context.Context, request MediaRetryRequest) (response MediaRetryResponse, err error)
}

// IMessageUsecase combines all message interfaces
type IMessageUsecase interface {
	IMessageActions
	IMessageManagement
}
