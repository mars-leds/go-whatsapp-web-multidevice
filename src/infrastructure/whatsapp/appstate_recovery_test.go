package whatsapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types/events"
)

func TestAppStateRecoveryLimiter(t *testing.T) {
	l := &appStateRecoveryLimiter{}
	t0 := time.Date(2026, time.November, 5, 18, 0, 0, 0, time.UTC)

	_, ok := l.reserve("dev|regular_low", t0)
	assert.True(t, ok, "first request goes through")

	since, ok := l.reserve("dev|regular_low", t0.Add(42*time.Second))
	assert.False(t, ok, "second request within the interval is refused")
	assert.Equal(t, 42*time.Second, since)

	_, ok = l.reserve("dev|regular", t0.Add(42*time.Second))
	assert.True(t, ok, "another collection has its own budget")
	_, ok = l.reserve("other|regular_low", t0.Add(42*time.Second))
	assert.True(t, ok, "another device has its own budget")

	_, ok = l.reserve("dev|regular_low", t0.Add(AppStateRecoveryMinInterval))
	assert.True(t, ok, "allowed again once the interval elapsed")

	l.release("dev|regular_low")
	_, ok = l.reserve("dev|regular_low", t0.Add(AppStateRecoveryMinInterval+time.Second))
	assert.True(t, ok, "released reservations do not count")
}

// fakeRecoverySubscription captures the handler so the fake send can feed
// it events, and records whether it was unsubscribed.
type fakeRecoverySubscription struct {
	handler      func(any)
	unsubscribed bool
}

func (f *fakeRecoverySubscription) subscribe(handler func(any)) func() {
	f.handler = handler
	return func() { f.unsubscribed = true }
}

func TestAwaitAppStateRecoveryAnswered(t *testing.T) {
	sub := &fakeRecoverySubscription{}
	send := func(context.Context) error {
		// Noise first, then the real answer (as whatsmeow dispatches it).
		sub.handler(&events.AppStateSyncComplete{Name: appstate.WAPatchRegular, Recovery: true, Version: 1})
		sub.handler(&events.AppStateSyncComplete{Name: appstate.WAPatchRegularLow, Recovery: false, Version: 2})
		sub.handler(&events.Connected{})
		sub.handler(&events.AppStateSyncComplete{Name: appstate.WAPatchRegularLow, Recovery: true, Version: 352})
		return nil
	}
	version, err := awaitAppStateRecovery(context.Background(), appstate.WAPatchRegularLow, time.Second, sub.subscribe, send)
	require.NoError(t, err)
	assert.Equal(t, uint64(352), version)
	assert.True(t, sub.unsubscribed)
}

func TestAwaitAppStateRecoveryTimeout(t *testing.T) {
	sub := &fakeRecoverySubscription{}
	sent := false
	send := func(context.Context) error { sent = true; return nil }
	start := time.Now()
	_, err := awaitAppStateRecovery(context.Background(), appstate.WAPatchRegularLow, 50*time.Millisecond, sub.subscribe, send)
	assert.True(t, sent)
	assert.ErrorIs(t, err, ErrAppStateRecoveryTimeout)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "WhatsApp must be open on the main phone")
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.True(t, sub.unsubscribed)
}

func TestAwaitAppStateRecoverySendFails(t *testing.T) {
	sub := &fakeRecoverySubscription{}
	boom := errors.New("websocket not connected")
	_, err := awaitAppStateRecovery(context.Background(), appstate.WAPatchRegularLow, time.Second, sub.subscribe, func(context.Context) error { return boom })
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrAppStateRecoveryTimeout)
	assert.True(t, sub.unsubscribed)
}

func TestRequestAppStateRecoveryNilClient(t *testing.T) {
	_, err := RequestAppStateRecovery(context.Background(), nil, "dev", appstate.WAPatchRegularLow)
	assert.Error(t, err)
}

// TestRequestAppStateRecoveryOfflineReleasesTheBudget: an offline client
// cannot send the peer message; the error is the send error (not the
// timeout) and the rate limit budget is given back so the next call can
// try again.
func TestRequestAppStateRecoveryOfflineReleasesTheBudget(t *testing.T) {
	client := newLabelSyncTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deviceID := "offline-recovery-test@s.whatsapp.net"

	_, err := RequestAppStateRecovery(ctx, client, deviceID, appstate.WAPatchRegularLow)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrAppStateRecoveryRateLimited)
	assert.NotErrorIs(t, err, ErrAppStateRecoveryTimeout)

	_, err = RequestAppStateRecovery(ctx, client, deviceID, appstate.WAPatchRegularLow)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrAppStateRecoveryRateLimited, "a failed send does not consume the budget")

	// A reservation that did go out (simulated) is enforced.
	_, ok := recoveryLimiter.reserve(deviceID+"|"+string(appstate.WAPatchRegularLow), time.Now())
	require.True(t, ok)
	_, err = RequestAppStateRecovery(ctx, client, deviceID, appstate.WAPatchRegularLow)
	assert.ErrorIs(t, err, ErrAppStateRecoveryRateLimited)
	assert.Contains(t, err.Error(), "retry in")
	recoveryLimiter.release(deviceID + "|" + string(appstate.WAPatchRegularLow))
}
