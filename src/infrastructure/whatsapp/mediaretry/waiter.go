// Package mediaretry provides an in-process registry that routes whatsmeow
// MediaRetry notifications back to the HTTP handler that triggered the
// retry via Client.SendMediaRetryReceipt.
//
// Flow:
//  1. HTTP handler calls Register(messageID) -> gets a channel.
//  2. Handler sends the media retry receipt and blocks on the channel.
//  3. The global whatsmeow event dispatcher calls Deliver(evt); the
//     registered channel receives the event.
//  4. Handler decrypts, downloads, and cleans up via Release.
//
// The waiter survives a single notification; duplicate events for the
// same message_id after delivery are dropped.
package mediaretry

import (
	"sync"

	"go.mau.fi/whatsmeow/types/events"
)

// Waiter is a single-use subscription to a MediaRetry event.
type Waiter struct {
	MessageID string
	Ch        chan *events.MediaRetry
}

var registry sync.Map // map[string]*Waiter

// Register creates a waiter for the given message_id. The caller is
// responsible for calling Release() once the wait is complete (success,
// timeout, or error) to avoid leaking entries.
//
// If a waiter for this message_id already exists, the previous one is
// replaced; whichever caller still holds the old channel will block
// forever unless it also set its own timeout, so callers MUST set one.
func Register(messageID string) *Waiter {
	w := &Waiter{
		MessageID: messageID,
		// Buffered so Deliver never blocks the whatsmeow event goroutine.
		Ch: make(chan *events.MediaRetry, 1),
	}
	registry.Store(messageID, w)
	return w
}

// Release removes the waiter. Safe to call multiple times.
func Release(messageID string) {
	registry.Delete(messageID)
}

// Deliver forwards a MediaRetry event to a matching waiter, if any.
// Returns true if the event was handed off; false otherwise (in which
// case the caller may log/ignore).
func Deliver(evt *events.MediaRetry) bool {
	if evt == nil {
		return false
	}
	value, ok := registry.Load(evt.MessageID)
	if !ok {
		return false
	}
	w, ok := value.(*Waiter)
	if !ok {
		return false
	}
	select {
	case w.Ch <- evt:
		return true
	default:
		// Channel already holds a notification; drop duplicates.
		return false
	}
}
