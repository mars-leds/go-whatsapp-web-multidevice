package message

// MediaRetryRequest triggers a WhatsApp "media retry" on an old message
// whose CDN URL has expired (typically >30 days old). The original sender
// is asked to re-upload the media and a new DirectPath is returned.
type MediaRetryRequest struct {
	MessageID string `json:"message_id" uri:"message_id"`
	Phone     string `json:"phone" form:"phone"`
	// TimeoutSeconds optionally overrides the default 30s wait for the
	// asynchronous events.MediaRetry notification.
	TimeoutSeconds int `json:"timeout_seconds" form:"timeout_seconds"`
}

// MediaRetryResponse mirrors DownloadMediaResponse so clients can treat
// both endpoints interchangeably once the retry succeeds.
type MediaRetryResponse struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
	MediaType string `json:"media_type"`
	Filename  string `json:"filename"`
	FilePath  string `json:"file_path"`
	FileSize  int64  `json:"file_size"`
}
