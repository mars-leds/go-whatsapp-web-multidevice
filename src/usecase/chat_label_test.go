package usecase

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

func TestLabelChatMessage(t *testing.T) {
	assert.Equal(t, "Chat labeled successfully with label 'Important'", labelChatMessage("Important", true))
	assert.Equal(t, "Label 'Important' removed from chat successfully", labelChatMessage("Important", false))
}

// TestLabelChatPatchShape pins the whatsmeow patch this usecase relies on:
// collection `regular`, index [label_jid, <label_id>, <chat_jid>], and a
// LabelAssociationAction carrying the `labeled` flag. If a whatsmeow bump
// changes BuildLabelChat, this is the test that should break first.
func TestLabelChatPatchShape(t *testing.T) {
	target := types.NewJID("6289685028129", types.DefaultUserServer)

	for _, labeled := range []bool{true, false} {
		patch := appstate.BuildLabelChat(target, "7", labeled)

		assert.Equal(t, appstate.WAPatchRegular, patch.Type)
		if assert.Len(t, patch.Mutations, 1) {
			mutation := patch.Mutations[0]
			assert.Equal(t, []string{appstate.IndexLabelAssociationChat, "7", target.String()}, mutation.Index)
			if assert.NotNil(t, mutation.Value) && assert.NotNil(t, mutation.Value.LabelAssociationAction) {
				assert.Equal(t, labeled, mutation.Value.LabelAssociationAction.GetLabeled())
			}
		}
	}
}

func TestLabelChatIndexJIDs(t *testing.T) {
	pn := types.NewJID("5491112345678", types.DefaultUserServer)
	lid := types.NewJID("223754944819424", "lid")

	assert.Equal(t, []types.JID{pn}, labelChatIndexJIDs(pn, types.EmptyJID, true), "no LID known: phone number")
	assert.Equal(t, []types.JID{pn}, labelChatIndexJIDs(pn, types.EmptyJID, false))
	assert.Equal(t, []types.JID{lid}, labelChatIndexJIDs(pn, lid, true), "apply: the phone's form only")
	assert.Equal(t, []types.JID{lid, pn}, labelChatIndexJIDs(pn, lid, false), "remove: both forms")
}

func TestBuildLabelChatPatchMultipleKeys(t *testing.T) {
	pn := types.NewJID("5491112345678", types.DefaultUserServer)
	lid := types.NewJID("223754944819424", "lid")

	patch := buildLabelChatPatch([]types.JID{lid, pn}, "13", false)
	assert.Equal(t, appstate.WAPatchRegular, patch.Type)
	require.Len(t, patch.Mutations, 2)
	assert.Equal(t, []string{"label_jid", "13", "223754944819424@lid"}, patch.Mutations[0].Index)
	assert.Equal(t, []string{"label_jid", "13", "5491112345678@s.whatsapp.net"}, patch.Mutations[1].Index)
	for _, m := range patch.Mutations {
		assert.False(t, m.Value.GetLabelAssociationAction().GetLabeled())
	}

	single := buildLabelChatPatch([]types.JID{pn}, "13", true)
	require.Len(t, single.Mutations, 1)
	assert.True(t, single.Mutations[0].Value.GetLabelAssociationAction().GetLabeled())
}
