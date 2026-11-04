package whatsapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func newLabelSyncTestClient(t *testing.T) *whatsmeow.Client {
	t.Helper()
	ensureTestLogger()
	container := newTestSQLStore(t)
	dev := newTestStoreDevice(container, types.NewADJID("6281000000001", types.WhatsAppDomain, 3), "labels")
	require.NoError(t, dev.Save(context.Background()))
	return whatsmeow.NewClient(dev, nil)
}

func TestLabelSyncCollectorKeepsOnlyFullSyncLabelEvents(t *testing.T) {
	c := &labelSyncCollector{}
	lid := types.NewJID("223754944819424", "lid")

	c.handle(&events.LabelEdit{
		LabelID:      "13",
		Timestamp:    time.UnixMilli(1_700_000_000), // phone-style seconds-as-ms
		Action:       &waSyncAction.LabelEditAction{Name: proto.String("ATENCION HUMANA")},
		FromFullSync: true,
	})
	c.handle(&events.LabelEdit{LabelID: "14", Action: &waSyncAction.LabelEditAction{Name: proto.String("live")}, FromFullSync: false})
	c.handle(&events.LabelEdit{LabelID: "15", Action: nil, FromFullSync: true})
	c.handle(&events.LabelEdit{LabelID: "", Action: &waSyncAction.LabelEditAction{}, FromFullSync: true})
	c.handle(&events.LabelAssociationChat{
		JID:          lid,
		LabelID:      "13",
		Timestamp:    time.UnixMilli(1_700_000_000_000),
		Action:       &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)},
		FromFullSync: true,
	})
	c.handle(&events.LabelAssociationChat{JID: lid, LabelID: "13", Action: &waSyncAction.LabelAssociationAction{}, FromFullSync: false})
	c.handle(&events.LabelAssociationChat{JID: types.EmptyJID, LabelID: "13", Action: &waSyncAction.LabelAssociationAction{}, FromFullSync: true})
	c.handle(&events.LabelAssociationChat{JID: lid, LabelID: "13", Action: nil, FromFullSync: true})
	c.handle(&events.Mute{FromFullSync: true})
	c.handle("not an event")

	require.Len(t, c.labels, 1)
	assert.Equal(t, "13", c.labels[0].LabelID)
	assert.Equal(t, time.Unix(1_700_000_000, 0), c.labels[0].Timestamp, "timestamp normalized (seconds)")

	require.Len(t, c.assocs, 1)
	assert.Equal(t, labelstate.Association{
		RawJID:    "223754944819424@lid",
		LabelID:   "13",
		Labeled:   true,
		Timestamp: time.UnixMilli(1_700_000_000_000),
	}, c.assocs[0])
}

func TestFullSyncLabelsRaisesFlagDuringFetchAndRestoresIt(t *testing.T) {
	client := newLabelSyncTestClient(t)
	state := LabelStateForClient(client)
	t.Cleanup(func() { labelstate.Forget(client) })
	require.False(t, client.EmitAppStateEventsOnFullSync, "upstream default")

	var seenDuringFetch bool
	err := fullSyncLabels(context.Background(), client, state, func(context.Context) error {
		seenDuringFetch = client.EmitAppStateEventsOnFullSync
		return nil
	})
	require.NoError(t, err)
	assert.True(t, seenDuringFetch, "flag must be on while FetchAppState runs")
	assert.False(t, client.EmitAppStateEventsOnFullSync, "flag restored after the call")
	assert.True(t, state.Synced(), "an empty but successful sync still marks the cache synced")
}

func TestFullSyncLabelsRestoresFlagAndKeepsCacheOnError(t *testing.T) {
	client := newLabelSyncTestClient(t)
	state := LabelStateForClient(client)
	t.Cleanup(func() { labelstate.Forget(client) })
	state.ApplyLabelEdit("1", &waSyncAction.LabelEditAction{Name: proto.String("kept")}, time.Now())

	boom := errors.New("boom")
	err := fullSyncLabels(context.Background(), client, state, func(context.Context) error { return boom })
	require.ErrorIs(t, err, boom)
	assert.False(t, client.EmitAppStateEventsOnFullSync)
	assert.False(t, state.Synced())
	require.Len(t, state.Labels(), 1, "a failed sync must not wipe the cache")
}

func TestFullSyncLabelsRealFetchFailsOffline(t *testing.T) {
	// A disconnected client cannot fetch the snapshot: the real FetchAppState
	// must surface that as an error (not hang, not panic) and leave the flag
	// restored.
	client := newLabelSyncTestClient(t)
	state := LabelStateForClient(client)
	t.Cleanup(func() { labelstate.Forget(client) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := FullSyncLabels(ctx, client, state)
	require.Error(t, err)
	assert.False(t, client.EmitAppStateEventsOnFullSync)
	assert.False(t, state.Synced())
}

func TestFullSyncLabelsNilClient(t *testing.T) {
	require.Error(t, FullSyncLabels(context.Background(), nil, nil))
}
