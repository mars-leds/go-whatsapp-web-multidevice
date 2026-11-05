package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types/events"
)

// Fork-specific: app state recovery from the main phone.
//
// When even a full sync of a collection fails LTHash verification (the
// server's own snapshot does not verify against the keys this device has:
// tulir/whatsmeow#858, old accounts, rotated app state keys, LID
// migrations), the only remaining repair short of unlinking every device
// is to ask the main phone for an unencrypted copy of the collection:
// SendPeerMessage(BuildAppStateRecoveryRequest(name)). The phone answers
// with a PeerDataOperationResult that whatsmeow processes on its own
// (handleAppStateRecovery: resets the local version, stores the phone's
// LTHash and MACs) and then dispatches
// events.AppStateSyncComplete{Name: name, Recovery: true}.
//
// Operational requirement: WhatsApp must be open on the main phone. With
// the app in the foreground the answer takes about a second; with it
// closed the request may never be answered, hence the timeout and the
// per-device, per-collection rate limit below.

const (
	// AppStateRecoveryTimeout bounds the wait for the phone's answer.
	AppStateRecoveryTimeout = 15 * time.Second
	// AppStateRecoveryMinInterval is the minimum spacing between two
	// recovery requests for the same device and collection.
	AppStateRecoveryMinInterval = 10 * time.Minute
)

var (
	// ErrAppStateRecoveryRateLimited: a recovery request for this device
	// and collection was sent less than AppStateRecoveryMinInterval ago.
	ErrAppStateRecoveryRateLimited = errors.New("app state recovery already requested recently")
	// ErrAppStateRecoveryTimeout: the phone did not answer in time.
	ErrAppStateRecoveryTimeout = errors.New("the phone did not answer the app state recovery request")
)

// appStateRecoveryLimiter remembers when the last recovery request went
// out per "<device>|<collection>".
type appStateRecoveryLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var recoveryLimiter = &appStateRecoveryLimiter{}

// reserve records now as the last request time for key unless one was
// recorded less than AppStateRecoveryMinInterval ago, in which case it
// reports how long ago that was.
func (l *appStateRecoveryLimiter) reserve(key string, now time.Time) (since time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, found := l.last[key]; found && now.Sub(last) < AppStateRecoveryMinInterval {
		return now.Sub(last), false
	}
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	l.last[key] = now
	return 0, true
}

// release forgets the reservation (the request could not be sent).
func (l *appStateRecoveryLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.last, key)
}

// RequestAppStateRecovery asks the main phone for a recovery copy of the
// collection and waits up to AppStateRecoveryTimeout for whatsmeow to
// apply it. It returns the version the collection has afterwards.
//
// The local version of the collection is reset before asking: whatsmeow
// ignores a recovery answer whose version is not newer than the local one,
// and after a failed write the local version is unreliable anyway. If the
// phone never answers the collection stays at version 0, which only means
// whatsmeow's next sync of it will be a full one (as it already was).
//
// Errors: ErrAppStateRecoveryRateLimited (nothing was sent),
// ErrAppStateRecoveryTimeout (sent, no answer), or the send error. The
// caller must hold the device's app state write mutex.
func RequestAppStateRecovery(ctx context.Context, client *whatsmeow.Client, deviceID string, name appstate.WAPatchName) (version uint64, err error) {
	if client == nil || client.Store == nil || client.Store.AppState == nil {
		return 0, errors.New("whatsapp client is not initialized")
	}
	key := deviceID + "|" + string(name)
	since, ok := recoveryLimiter.reserve(key, time.Now())
	if !ok {
		return 0, fmt.Errorf("%w: %s for %s was requested %s ago, retry in %s",
			ErrAppStateRecoveryRateLimited, name, deviceID, since.Round(time.Second), (AppStateRecoveryMinInterval - since).Round(time.Second))
	}

	previous, _, verr := client.Store.AppState.GetAppStateVersion(ctx, string(name))
	if verr != nil {
		logrus.WithError(verr).WithField("collection", name).Warn("Could not read the app state version before the recovery request")
	}
	if err = client.Store.AppState.DeleteAppStateVersion(ctx, string(name)); err != nil {
		recoveryLimiter.release(key)
		return 0, fmt.Errorf("failed to reset app state %s version before the recovery request: %w", name, err)
	}
	logrus.WithFields(logrus.Fields{
		"device_id":        deviceID,
		"collection":       name,
		"previous_version": previous,
	}).Info("Requesting an app state recovery copy from the main phone")

	version, err = awaitAppStateRecovery(ctx, name, AppStateRecoveryTimeout,
		func(handler func(any)) func() {
			id := client.AddEventHandler(handler)
			return func() { client.RemoveEventHandler(id) }
		},
		func(ctx context.Context) error {
			_, err := client.SendPeerMessage(ctx, whatsmeow.BuildAppStateRecoveryRequest(name))
			return err
		})
	if err != nil {
		if !errors.Is(err, ErrAppStateRecoveryTimeout) {
			// Nothing reached the phone: let the next call try again.
			recoveryLimiter.release(key)
		}
		logrus.WithError(err).WithFields(logrus.Fields{"device_id": deviceID, "collection": name}).Warn("App state recovery failed")
		return 0, err
	}
	logrus.WithFields(logrus.Fields{
		"device_id":        deviceID,
		"collection":       name,
		"previous_version": previous,
		"version":          version,
	}).Info("App state recovery copy applied")
	return version, nil
}

// awaitAppStateRecovery is the event-waiting core of RequestAppStateRecovery
// with the subscription and the send injected for tests: it subscribes a
// handler for AppStateSyncComplete{Name: name, Recovery: true}, sends, and
// waits up to timeout for the event.
func awaitAppStateRecovery(ctx context.Context, name appstate.WAPatchName, timeout time.Duration, subscribe func(handler func(any)) (unsubscribe func()), send func(context.Context) error) (uint64, error) {
	done := make(chan uint64, 1)
	unsubscribe := subscribe(func(evt any) {
		if complete, ok := evt.(*events.AppStateSyncComplete); ok && complete.Recovery && complete.Name == name {
			select {
			case done <- complete.Version:
			default:
			}
		}
	})
	defer unsubscribe()

	if err := send(ctx); err != nil {
		return 0, fmt.Errorf("failed to send the app state recovery request for %s: %w", name, err)
	}
	logrus.WithField("collection", name).Debug("App state recovery request sent, waiting for the phone")

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case version := <-done:
		return version, nil
	case <-waitCtx.Done():
		return 0, fmt.Errorf("%w for %s within %s: WhatsApp must be open on the main phone (%w)", ErrAppStateRecoveryTimeout, name, timeout, waitCtx.Err())
	}
}
