package mediaretry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/types/events"
)

// Deliver only reports success after a buffered send, so a successful
// Deliver guarantees the receive below never blocks.

func TestDeliverRoutesToRegisteredWaiter(t *testing.T) {
	w := Register("msg-deliver")
	defer w.Release()

	evt := &events.MediaRetry{MessageID: "msg-deliver"}
	require.True(t, Deliver(evt))
	assert.Same(t, evt, <-w.Ch)
}

func TestDeliverWithoutWaiter(t *testing.T) {
	assert.False(t, Deliver(&events.MediaRetry{MessageID: "msg-nobody"}))
	assert.False(t, Deliver(nil))
}

func TestDeliverDropsDuplicates(t *testing.T) {
	w := Register("msg-dup")
	defer w.Release()

	assert.True(t, Deliver(&events.MediaRetry{MessageID: "msg-dup"}))
	assert.False(t, Deliver(&events.MediaRetry{MessageID: "msg-dup"}))
}

func TestReleaseOfSupersededWaiterKeepsNewerOne(t *testing.T) {
	older := Register("msg-race")
	newer := Register("msg-race")
	defer newer.Release()

	// The older caller finishing (e.g. timing out) must not unregister the
	// newer caller's waiter.
	older.Release()

	evt := &events.MediaRetry{MessageID: "msg-race"}
	require.True(t, Deliver(evt))
	assert.Same(t, evt, <-newer.Ch)
}

func TestReleaseUnregisters(t *testing.T) {
	w := Register("msg-release")
	w.Release()
	w.Release()

	assert.False(t, Deliver(&events.MediaRetry{MessageID: "msg-release"}))
}
