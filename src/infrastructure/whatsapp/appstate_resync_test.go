package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
)

func TestIsAppStateOutOfSync(t *testing.T) {
	// The exact shape whatsmeow produces on a 409 whose conflict patches
	// fail LTHash verification (SOPORTE_FABIAN, 2026-11-05).
	mainErr := fmt.Errorf("%w (%s): %s", whatsmeow.ErrAppStateUpdate, appstate.WAPatchRegularLow, `<error code="409" text="conflict"/>`)
	apply := fmt.Errorf("failed to decode app state regular_low patches: %w", fmt.Errorf("failed to verify patch v188: %w", appstate.ErrMismatchingLTHash))
	combined := fmt.Errorf("%w (also, applying patches in the response failed: %w)", mainErr, apply)

	assert.True(t, IsAppStateOutOfSync(combined))
	assert.True(t, IsAppStateOutOfSync(mainErr), "a bare 409 counts")
	assert.True(t, IsAppStateOutOfSync(apply), "a bare LTHash mismatch counts")
	assert.False(t, IsAppStateOutOfSync(nil))
	assert.False(t, IsAppStateOutOfSync(errors.New("no app state keys found")))
	assert.False(t, IsAppStateOutOfSync(context.Canceled))
	assert.False(t, IsAppStateOutOfSync(whatsmeow.ErrNotLoggedIn))
}

func TestFullSyncAppStateNilClient(t *testing.T) {
	_, err := FullSyncAppState(context.Background(), nil, appstate.WAPatchRegularLow)
	assert.Error(t, err)
	_, err = FullSyncAppState(context.Background(), &whatsmeow.Client{}, appstate.WAPatchRegularLow)
	assert.Error(t, err, "client without a store")
}
