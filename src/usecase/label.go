package usecase

import (
	"context"
	"fmt"
	"sort"
	"time"

	domainLabel "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/label"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/validations"
	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Fork-specific: WhatsApp (Business) labels API. See FORK_CHANGES.md.
//
// Every operation takes the per-client label mutex (labelstate.State.Lock)
// so a full sync never races a SendAppState on the `regular` collection,
// and POST /chat/{jid}/label shares the same mutex (chat_label.go).

type serviceLabel struct{}

func NewLabelService() domainLabel.ILabelUsecase {
	return &serviceLabel{}
}

// labelEditMutationVersion is the `label_edit` mutation version whatsmeow's
// appstate.BuildLabelEdit uses; pinned by TestLabelEditPatchShape.
const labelEditMutationVersion int32 = 3

func (service serviceLabel) ListLabels(ctx context.Context, request domainLabel.ListLabelsRequest) (response domainLabel.ListLabelsResponse, err error) {
	client, state, err := labelClientAndState(ctx)
	if err != nil {
		return response, err
	}
	state.Lock()
	defer state.Unlock()

	if request.Refresh || !state.Synced() {
		if err = whatsapp.FullSyncLabels(ctx, client, state); err != nil {
			logrus.WithError(err).Error("Failed to full-sync labels")
			return response, pkgError.InternalServerError(err.Error())
		}
		response.Refreshed = true
	}

	labels := state.Labels()
	response.Labels = make([]domainLabel.Label, 0, len(labels))
	deleted := make(map[string]bool, len(labels))
	for _, l := range labels {
		response.Labels = append(response.Labels, toDomainLabel(l))
		if l.Action.GetDeleted() {
			deleted[l.LabelID] = true
		}
	}
	response.Chats = normalizeAssociations(state.Associations(), deleted, func(lid types.JID) types.JID {
		return whatsapp.NormalizeJIDFromLID(ctx, lid, client)
	})
	return response, nil
}

func (service serviceLabel) CreateLabel(ctx context.Context, request domainLabel.CreateLabelRequest) (response domainLabel.CreateLabelResponse, err error) {
	if err = validations.ValidateCreateLabel(ctx, &request); err != nil {
		return response, err
	}
	client, state, err := labelClientAndState(ctx)
	if err != nil {
		return response, err
	}
	state.Lock()
	defer state.Unlock()

	// The id is chosen by the writer: max known id + 1, so the cache must be
	// complete (deleted labels keep their ids).
	if err = ensureLabelsSynced(ctx, client, state); err != nil {
		return response, err
	}

	var color int32
	if request.Color != nil {
		color = *request.Color
	}
	labelID := state.NextLabelID()
	action := newCustomLabelAction(request.Name, color, state.NextOrderIndex())

	if err = sendLabelEdit(ctx, client, state, labelID, action); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"label_id": labelID, "name": request.Name}).Error("Failed to create label")
		return response, err
	}

	response.Status = "success"
	response.Message = fmt.Sprintf("Label '%s' created with id %s", request.Name, labelID)
	response.Label = toDomainLabel(labelstate.Label{LabelID: labelID, Action: action})
	logrus.WithFields(logrus.Fields{"label_id": labelID, "name": request.Name, "color": color}).Info("Label created")
	return response, nil
}

func (service serviceLabel) UpdateLabel(ctx context.Context, request domainLabel.UpdateLabelRequest) (response domainLabel.UpdateLabelResponse, err error) {
	if err = validations.ValidateUpdateLabel(ctx, &request); err != nil {
		return response, err
	}
	client, state, err := labelClientAndState(ctx)
	if err != nil {
		return response, err
	}
	state.Lock()
	defer state.Unlock()

	existing, err := knownLabel(ctx, client, state, request.LabelID)
	if err != nil {
		return response, err
	}

	// A SET replaces the whole record: resend every field of the last known
	// state with only name/color changed.
	action := existing.Action
	if request.Name != nil {
		action.Name = proto.String(*request.Name)
	}
	if request.Color != nil {
		action.Color = proto.Int32(*request.Color)
	}

	if err = sendLabelEdit(ctx, client, state, request.LabelID, action); err != nil {
		logrus.WithError(err).WithField("label_id", request.LabelID).Error("Failed to update label")
		return response, err
	}

	response.Status = "success"
	response.Message = fmt.Sprintf("Label %s updated", request.LabelID)
	response.Label = toDomainLabel(labelstate.Label{LabelID: request.LabelID, Action: action})
	logrus.WithFields(logrus.Fields{"label_id": request.LabelID, "name": action.GetName(), "color": action.GetColor()}).Info("Label updated")
	return response, nil
}

func (service serviceLabel) DeleteLabel(ctx context.Context, request domainLabel.DeleteLabelRequest) (response domainLabel.DeleteLabelResponse, err error) {
	if err = validations.ValidateDeleteLabel(ctx, &request); err != nil {
		return response, err
	}
	client, state, err := labelClientAndState(ctx)
	if err != nil {
		return response, err
	}
	state.Lock()
	defer state.Unlock()

	existing, err := knownLabel(ctx, client, state, request.LabelID)
	if err != nil {
		return response, err
	}

	action := existing.Action
	action.Deleted = proto.Bool(true)

	if err = sendLabelEdit(ctx, client, state, request.LabelID, action); err != nil {
		logrus.WithError(err).WithField("label_id", request.LabelID).Error("Failed to delete label")
		return response, err
	}

	response.Status = "success"
	response.Message = fmt.Sprintf("Label '%s' (%s) deleted", action.GetName(), request.LabelID)
	response.Label = toDomainLabel(labelstate.Label{LabelID: request.LabelID, Action: action})
	logrus.WithFields(logrus.Fields{"label_id": request.LabelID, "name": action.GetName()}).Info("Label deleted")
	return response, nil
}

// labelClientAndState resolves the client of the request's device, requires
// it to be logged in and returns its label cache.
func labelClientAndState(ctx context.Context) (*whatsmeow.Client, *labelstate.State, error) {
	client := whatsapp.ClientFromContext(ctx)
	if client == nil {
		return nil, nil, pkgError.ErrWaCLI
	}
	utils.MustLogin(client)
	return client, whatsapp.LabelStateForClient(client), nil
}

// ensureLabelsSynced runs a full sync unless the cache already had one.
// Caller holds state.Lock().
func ensureLabelsSynced(ctx context.Context, client *whatsmeow.Client, state *labelstate.State) error {
	if state.Synced() {
		return nil
	}
	if err := whatsapp.FullSyncLabels(ctx, client, state); err != nil {
		logrus.WithError(err).Error("Failed to full-sync labels")
		return pkgError.InternalServerError(err.Error())
	}
	return nil
}

// knownLabel returns the last known record of a label, syncing first if the
// cache is partial. Deleted labels are returned too (a client may want to
// rename/undelete; the Deleted flag is visible in the response).
func knownLabel(ctx context.Context, client *whatsmeow.Client, state *labelstate.State, labelID string) (labelstate.Label, error) {
	if err := ensureLabelsSynced(ctx, client, state); err != nil {
		return labelstate.Label{}, err
	}
	existing, ok := state.Label(labelID)
	if !ok {
		return labelstate.Label{}, pkgError.ErrLabelNotFound
	}
	return existing, nil
}

// newCustomLabelAction builds the full record the phone writes for one of
// its own labels (name, color, not deleted, active, CUSTOM, order index).
// appstate.BuildLabelEdit only sets name/color/deleted; sending that alone
// leaves is_active/type unset on the server.
func newCustomLabelAction(name string, color, orderIndex int32) *waSyncAction.LabelEditAction {
	return &waSyncAction.LabelEditAction{
		Name:       proto.String(name),
		Color:      proto.Int32(color),
		Deleted:    proto.Bool(false),
		OrderIndex: proto.Int32(orderIndex),
		IsActive:   proto.Bool(true),
		Type:       waSyncAction.LabelEditAction_CUSTOM.Enum(),
	}
}

// labelEditPatch is appstate.BuildLabelEdit with a caller-supplied full
// LabelEditAction.
func labelEditPatch(labelID string, action *waSyncAction.LabelEditAction) appstate.PatchInfo {
	return appstate.PatchInfo{
		Type: appstate.WAPatchRegular,
		Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexLabelEdit, labelID},
			Version: labelEditMutationVersion,
			Value:   &waSyncAction.SyncActionValue{LabelEditAction: action},
		}},
	}
}

// sendLabelEdit pushes the record and applies it to the cache optimistically
// (the resync whatsmeow triggers after SendAppState confirms it through the
// normal event path). Caller holds state.Lock().
func sendLabelEdit(ctx context.Context, client *whatsmeow.Client, state *labelstate.State, labelID string, action *waSyncAction.LabelEditAction) error {
	if err := client.SendAppState(ctx, labelEditPatch(labelID, action)); err != nil {
		return err
	}
	state.ApplyLabelEdit(labelID, action, time.Now())
	return nil
}

func toDomainLabel(l labelstate.Label) domainLabel.Label {
	a := l.Action
	out := domainLabel.Label{
		LabelID:      l.LabelID,
		Name:         a.GetName(),
		Color:        a.GetColor(),
		Deleted:      a.GetDeleted(),
		IsActive:     a.GetIsActive(),
		PredefinedID: a.GetPredefinedID(),
		OrderIndex:   a.GetOrderIndex(),
	}
	if a.Type != nil {
		out.Type = a.GetType().String()
	}
	return out
}

// normalizeAssociations turns the raw per-index records into one row per
// (chat, label): LIDs are resolved to phone numbers (keeping the LID in
// chat_lid), the PN and LID forms of the same chat are merged with the most
// recent record winning (ties: labeled if either is), and labels marked
// deleted are dropped (WhatsApp leaves their associations behind).
func normalizeAssociations(raw []labelstate.Association, deletedLabels map[string]bool, resolveLID func(types.JID) types.JID) []domainLabel.ChatLabel {
	type key struct{ chat, label string }
	merged := make(map[key]domainLabel.ChatLabel)
	latest := make(map[key]time.Time)

	for _, a := range raw {
		if deletedLabels[a.LabelID] {
			continue
		}
		jid, err := types.ParseJID(a.RawJID)
		if err != nil || jid.IsEmpty() || jid.User == "" {
			continue
		}
		jid = jid.ToNonAD()
		var chatLID string
		if jid.Server == "lid" {
			chatLID = jid.String()
			if resolveLID != nil {
				jid = resolveLID(jid).ToNonAD()
			}
		}
		k := key{chat: jid.String(), label: a.LabelID}
		row, seen := merged[k]
		switch {
		case !seen:
			row = domainLabel.ChatLabel{ChatID: k.chat, LabelID: a.LabelID, Labeled: a.Labeled}
			latest[k] = a.Timestamp
		case a.Timestamp.After(latest[k]):
			row.Labeled = a.Labeled
			latest[k] = a.Timestamp
		case a.Timestamp.Equal(latest[k]):
			row.Labeled = row.Labeled || a.Labeled
		}
		if chatLID != "" {
			row.ChatLID = chatLID
		}
		merged[k] = row
	}

	out := make([]domainLabel.ChatLabel, 0, len(merged))
	for _, row := range merged {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChatID != out[j].ChatID {
			return out[i].ChatID < out[j].ChatID
		}
		return out[i].LabelID < out[j].LabelID
	})
	return out
}
