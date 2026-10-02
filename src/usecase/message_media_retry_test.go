package usecase

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mau.fi/whatsmeow"
)

func TestMapMediaType(t *testing.T) {
	tests := []struct {
		kind      string
		mediaType whatsmeow.MediaType
		mmsType   string
		ok        bool
	}{
		{"image", whatsmeow.MediaImage, "image", true},
		{"video", whatsmeow.MediaVideo, "video", true},
		{"video_note", whatsmeow.MediaVideo, "video", true},
		{"audio", whatsmeow.MediaAudio, "audio", true},
		{"document", whatsmeow.MediaDocument, "document", true},
		{"sticker", whatsmeow.MediaImage, "image", true},
		{"call", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			mediaType, mmsType, ok := mapMediaType(tt.kind)
			assert.Equal(t, tt.mediaType, mediaType)
			assert.Equal(t, tt.mmsType, mmsType)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func TestMediaExtensionFor(t *testing.T) {
	assert.Equal(t, ".pdf", mediaExtensionFor("document", "invoice.pdf"))
	assert.Equal(t, ".mp4", mediaExtensionFor("video_note", ""))
	assert.Equal(t, ".webp", mediaExtensionFor("sticker", ""))
	assert.Equal(t, "", mediaExtensionFor("document", "README"))
}
