package whatsapp

import (
	"context"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// LIDForPN returns the LID whatsmeow has mapped for a phone-number JID, or
// an empty JID when the mapping is unknown (never seen that contact), the
// JID is not a user JID (groups, newsletters) or the store is unavailable.
//
// Fork-specific. The phone indexes chat<->label associations by the chat's
// LID, so POST /chat/{jid}/label resolves it to write the same index key.
func LIDForPN(ctx context.Context, pn types.JID, client *whatsmeow.Client) types.JID {
	if pn.Server == "lid" {
		return pn.ToNonAD()
	}
	if pn.Server != types.DefaultUserServer {
		return types.EmptyJID
	}
	if client == nil || client.Store == nil || client.Store.LIDs == nil {
		return types.EmptyJID
	}
	lid, err := client.Store.LIDs.GetLIDForPN(ctx, pn.ToNonAD())
	if err != nil {
		log.Debugf("Failed to resolve LID for %s: %v", pn.String(), err)
		return types.EmptyJID
	}
	if lid.IsEmpty() {
		return types.EmptyJID
	}
	return lid.ToNonAD()
}
