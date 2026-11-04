package usecase

import (
	"context"
	"testing"
	"time"

	domainLabel "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/label"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp/labelstate"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// TestLabelEditPatchShape pins that our full-record patch has the same
// collection / index / version as whatsmeow's appstate.BuildLabelEdit, so a
// whatsmeow bump that changes the mutation version breaks here first.
func TestLabelEditPatchShape(t *testing.T) {
	ref := appstate.BuildLabelEdit("7", "X", 1, false)
	ours := labelEditPatch("7", newCustomLabelAction("X", 1, 0))

	assert.Equal(t, ref.Type, ours.Type)
	require.Len(t, ours.Mutations, 1)
	assert.Equal(t, ref.Mutations[0].Index, ours.Mutations[0].Index)
	assert.Equal(t, ref.Mutations[0].Version, ours.Mutations[0].Version)
	assert.Equal(t, []string{"label_edit", "7"}, ours.Mutations[0].Index)

	action := ours.Mutations[0].Value.GetLabelEditAction()
	require.NotNil(t, action)
	assert.Equal(t, "X", action.GetName())
	assert.Equal(t, int32(1), action.GetColor())
	assert.False(t, action.GetDeleted())
	assert.True(t, action.GetIsActive())
	assert.Equal(t, waSyncAction.LabelEditAction_CUSTOM, action.GetType())
	assert.Equal(t, int32(0), action.GetOrderIndex())
	assert.Nil(t, action.PredefinedID)
}

func TestToDomainLabel(t *testing.T) {
	l := labelstate.Label{LabelID: "13", Action: &waSyncAction.LabelEditAction{
		Name:         proto.String("ATENCION HUMANA"),
		Color:        proto.Int32(4),
		Deleted:      proto.Bool(false),
		IsActive:     proto.Bool(true),
		Type:         waSyncAction.LabelEditAction_CUSTOM.Enum(),
		PredefinedID: proto.Int32(0),
		OrderIndex:   proto.Int32(2),
	}}
	assert.Equal(t, domainLabel.Label{
		LabelID: "13", Name: "ATENCION HUMANA", Color: 4, Deleted: false, IsActive: true,
		Type: "CUSTOM", PredefinedID: 0, OrderIndex: 2,
	}, toDomainLabel(l))

	bare := labelstate.Label{LabelID: "1", Action: &waSyncAction.LabelEditAction{Name: proto.String("x")}}
	assert.Equal(t, "", toDomainLabel(bare).Type, "type stays empty when WhatsApp did not send it")
}

func TestNormalizeAssociations(t *testing.T) {
	lid := types.NewJID("223754944819424", "lid")
	pn := types.NewJID("5491112345678", types.DefaultUserServer)
	resolve := func(j types.JID) types.JID {
		if j.User == lid.User {
			return pn
		}
		return j
	}
	t0 := time.Unix(1_700_000_000, 0)

	raw := []labelstate.Association{
		// phone wrote LID=true, the old fork later wrote PN=false: latest wins
		{RawJID: lid.String(), LabelID: "13", Labeled: true, Timestamp: t0},
		{RawJID: pn.String(), LabelID: "13", Labeled: false, Timestamp: t0.Add(time.Minute)},
		// same chat, other label: PN=false older, LID=true newer
		{RawJID: pn.String(), LabelID: "14", Labeled: false, Timestamp: t0},
		{RawJID: lid.String(), LabelID: "14", Labeled: true, Timestamp: t0.Add(time.Minute)},
		// tie: labeled if either says so
		{RawJID: pn.String(), LabelID: "15", Labeled: true, Timestamp: t0},
		{RawJID: lid.String(), LabelID: "15", Labeled: false, Timestamp: t0},
		// unresolvable LID stays as chat_id
		{RawJID: "999@lid", LabelID: "13", Labeled: true, Timestamp: t0},
		// group, device suffix dropped on users
		{RawJID: "120363424051089958@g.us", LabelID: "13", Labeled: true, Timestamp: t0},
		{RawJID: "6289685028129:3@s.whatsapp.net", LabelID: "13", Labeled: true, Timestamp: t0},
		// deleted label and garbage are dropped
		{RawJID: pn.String(), LabelID: "99", Labeled: true, Timestamp: t0},
		{RawJID: "not a jid", LabelID: "13", Labeled: true, Timestamp: t0},
	}

	got := normalizeAssociations(raw, map[string]bool{"99": true}, resolve)
	assert.Equal(t, []domainLabel.ChatLabel{
		{ChatID: "120363424051089958@g.us", LabelID: "13", Labeled: true},
		{ChatID: "5491112345678@s.whatsapp.net", ChatLID: lid.String(), LabelID: "13", Labeled: false},
		{ChatID: "5491112345678@s.whatsapp.net", ChatLID: lid.String(), LabelID: "14", Labeled: true},
		{ChatID: "5491112345678@s.whatsapp.net", ChatLID: lid.String(), LabelID: "15", Labeled: true},
		{ChatID: "6289685028129@s.whatsapp.net", LabelID: "13", Labeled: true},
		{ChatID: "999@lid", ChatLID: "999@lid", LabelID: "13", Labeled: true},
	}, got)

	assert.Empty(t, normalizeAssociations(nil, nil, nil))
	// nil resolver keeps the LID
	assert.Equal(t, "223754944819424@lid", normalizeAssociations([]labelstate.Association{{RawJID: lid.String(), LabelID: "1", Labeled: true}}, nil, nil)[0].ChatID)
}

func TestLabelUsecaseRequiresClient(t *testing.T) {
	// No device in context and no global client: every operation fails
	// before touching WhatsApp, and validation errors come first.
	svc := NewLabelService()
	ctx := context.Background()

	_, err := svc.ListLabels(ctx, domainLabel.ListLabelsRequest{})
	assert.Equal(t, pkgError.ErrWaCLI, err)

	_, err = svc.CreateLabel(ctx, domainLabel.CreateLabelRequest{})
	assert.Equal(t, pkgError.ValidationError("name: cannot be blank."), err)
	_, err = svc.CreateLabel(ctx, domainLabel.CreateLabelRequest{Name: "X"})
	assert.Equal(t, pkgError.ErrWaCLI, err)

	_, err = svc.UpdateLabel(ctx, domainLabel.UpdateLabelRequest{LabelID: "1"})
	assert.Equal(t, pkgError.ValidationError("at least one of name or color is required."), err)
	name := "X"
	_, err = svc.UpdateLabel(ctx, domainLabel.UpdateLabelRequest{LabelID: "1", Name: &name})
	assert.Equal(t, pkgError.ErrWaCLI, err)

	_, err = svc.DeleteLabel(ctx, domainLabel.DeleteLabelRequest{})
	assert.Equal(t, pkgError.ValidationError("label_id: cannot be blank."), err)
	_, err = svc.DeleteLabel(ctx, domainLabel.DeleteLabelRequest{LabelID: "1"})
	assert.Equal(t, pkgError.ErrWaCLI, err)
}
