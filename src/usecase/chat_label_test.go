package usecase

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
