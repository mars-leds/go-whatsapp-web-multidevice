package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types/events"
)

// Fork-specific: label listing support for GET /labels (see FORK_CHANGES.md).

// LabelStateForClient returns the per-client label cache / operation mutex.
func LabelStateForClient(client *whatsmeow.Client) *labelstate.State {
	if client == nil {
		return labelstate.For(nil)
	}
	return labelstate.For(client)
}

// LabelStateFromContext returns the label cache of the client in the
// device context (falling back to the global client, like ClientFromContext).
func LabelStateFromContext(ctx context.Context) *labelstate.State {
	return LabelStateForClient(ClientFromContext(ctx))
}

// labelSyncCollector gathers the label events a full sync re-emits.
type labelSyncCollector struct {
	labels []labelstate.Label
	assocs []labelstate.Association
}

// handle is the temporary event handler. Only full-sync events are
// collected: an incremental event that happens to arrive while the full
// sync runs is handled by the main handler as usual.
func (c *labelSyncCollector) handle(rawEvt any) {
	switch evt := rawEvt.(type) {
	case *events.LabelEdit:
		if !evt.FromFullSync || evt.Action == nil || evt.LabelID == "" {
			return
		}
		c.labels = append(c.labels, labelstate.Label{
			LabelID:   evt.LabelID,
			Action:    evt.Action,
			Timestamp: labelstate.NormalizeEventTime(evt.Timestamp),
		})
	case *events.LabelAssociationChat:
		if !evt.FromFullSync || evt.Action == nil || evt.LabelID == "" || evt.JID.IsEmpty() {
			return
		}
		c.assocs = append(c.assocs, labelstate.Association{
			RawJID:    evt.JID.String(),
			LabelID:   evt.LabelID,
			Labeled:   evt.Action.GetLabeled(),
			Timestamp: labelstate.NormalizeEventTime(evt.Timestamp),
		})
	}
}

// FullSyncLabels re-downloads the `regular` app state collection (the one
// that carries `label_edit` / `label_jid` / `label_message`, and nothing
// else: mute, pin, archive and contacts live in other collections) and
// replaces the client's label cache with what the server returned.
//
// whatsmeow never persists label names or associations, so a full sync is
// the only way to list them. By default a full sync emits no events
// (Client.EmitAppStateEventsOnFullSync is false and upstream gowa never sets
// it); the flag is raised only for the duration of this call and restored
// afterwards. With it on, FetchAppState dispatches every LabelEdit /
// LabelAssociationChat of the snapshot (flagged FromFullSync) synchronously
// before returning, so a temporary handler collects them all. The main
// handler (event_label.go) skips FromFullSync events, so the listing does
// not re-fire a webhook per historical record.
//
// The caller must hold state.Lock(): a SendAppState racing the full sync
// (which resets the local collection version to 0 before refetching) can
// be rejected by the server.
func FullSyncLabels(ctx context.Context, client *whatsmeow.Client, state *labelstate.State) error {
	if client == nil {
		return errors.New("whatsapp client is not initialized")
	}
	if state == nil {
		state = LabelStateForClient(client)
	}
	return fullSyncLabels(ctx, client, state, func(ctx context.Context) error {
		return client.FetchAppState(ctx, appstate.WAPatchRegular, true, false)
	})
}

// fullSyncLabels is FullSyncLabels with the fetch injected for tests.
func fullSyncLabels(ctx context.Context, client *whatsmeow.Client, state *labelstate.State, fetch func(context.Context) error) error {
	collector := &labelSyncCollector{}
	handlerID := client.AddEventHandler(collector.handle)
	defer client.RemoveEventHandler(handlerID)

	previous := client.EmitAppStateEventsOnFullSync
	client.EmitAppStateEventsOnFullSync = true
	defer func() { client.EmitAppStateEventsOnFullSync = previous }()

	if err := fetch(ctx); err != nil {
		return fmt.Errorf("full sync of app state %s failed: %w", appstate.WAPatchRegular, err)
	}

	state.Replace(collector.labels, collector.assocs)
	log.Infof("Label full sync completed: %d labels, %d chat associations", len(collector.labels), len(collector.assocs))
	return nil
}
