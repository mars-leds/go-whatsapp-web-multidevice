package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
)

// Fork-specific: recovery from an app state collection whose local copy has
// drifted from the server's.
//
// Old accounts (many client versions, years of patches) can end up with a
// local version/LTHash of a collection that the server no longer accepts:
// SendAppState then gets a 409, whatsmeow tries to apply the conflicting
// patches it was sent and fails with appstate.ErrMismatchingLTHash, and the
// regular incremental sync only logs that same error forever (whatsmeow
// never full-syncs on its own). The only repair is a full sync: reset the
// local version and download the snapshot again.

// IsAppStateOutOfSync reports whether err is the server rejecting an app
// state write (whatsmeow.ErrAppStateUpdate, HTTP-like 409) or a local
// LTHash verification failure (appstate.ErrMismatchingLTHash), the two
// faces of a drifted collection. Both are wrapped with %w by whatsmeow.
func IsAppStateOutOfSync(err error) bool {
	return err != nil && (errors.Is(err, whatsmeow.ErrAppStateUpdate) || errors.Is(err, appstate.ErrMismatchingLTHash))
}

// FullSyncAppState resets the local version of the collection and downloads
// its snapshot again (client.FetchAppState with fullSync=true). It returns
// the local version the collection had before the reset, for logging.
//
// Unlike FullSyncLabels this leaves Client.EmitAppStateEventsOnFullSync
// alone, so with gowa's default (false) the snapshot emits no events and
// therefore fires no webhook. It only touches the given collection: a
// `regular_low` resync (pin/archive/markChatAsRead) never affects labels,
// which live in `regular`.
//
// The caller must hold the device's app state write mutex
// (LabelStateForClient(client).Lock()) so no SendAppState races the reset.
func FullSyncAppState(ctx context.Context, client *whatsmeow.Client, name appstate.WAPatchName) (previousVersion uint64, err error) {
	if client == nil || client.Store == nil || client.Store.AppState == nil {
		return 0, errors.New("whatsapp client is not initialized")
	}
	previousVersion, _, err = client.Store.AppState.GetAppStateVersion(ctx, string(name))
	if err != nil {
		logrus.WithError(err).WithField("collection", name).Warn("Could not read the app state version before the full sync")
		previousVersion = 0
	}
	if err = client.FetchAppState(ctx, name, true, false); err != nil {
		return previousVersion, fmt.Errorf("full sync of app state %s failed: %w", name, err)
	}
	return previousVersion, nil
}
