package usecase

import (
	"context"
	"fmt"
	"time"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/validations"
	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

// LabelChat applies or removes a WhatsApp (Business) label on a chat.
//
// Fork-specific. It works like PinChat / ArchiveChat: an app state patch on
// the `regular` collection (index `label_jid`) pushed with SendAppState. The
// phone propagates the association to every linked device and whatsmeow
// emits the matching *events.LabelAssociationChat, which event_label.go
// turns into a `label.association` webhook event and applies to the label
// cache.
//
// Index form (LID vs phone number): the phone writes `label_jid` with the
// chat's LID, while earlier versions of this fork wrote the phone-number
// JID. Those are two different keys that can coexist, so:
//   - applying writes the LID form when whatsmeow knows the chat's LID
//     (falling back to the phone number otherwise), i.e. the same key the
//     phone would write;
//   - removing writes BOTH forms (one patch, two mutations) so a label the
//     phone put under the LID, or one an older build put under the number,
//     is cleared either way.
//
// The label must already exist (POST /labels creates one).
func (service serviceChat) LabelChat(ctx context.Context, request domainChat.LabelChatRequest) (response domainChat.LabelChatResponse, err error) {
	if err = validations.ValidateLabelChat(ctx, &request); err != nil {
		return response, err
	}
	labeled := *request.Labeled

	client := whatsapp.ClientFromContext(ctx)
	if client == nil {
		return response, pkgError.ErrWaCLI
	}

	// Validate JID and ensure connection (resolves an @lid input to its PN)
	targetJID, err := utils.ValidateJidWithLogin(client, request.ChatJID)
	if err != nil {
		return response, err
	}
	targetJID = targetJID.ToNonAD()

	// Serialize with GET /labels full syncs and the other label writes.
	state := whatsapp.LabelStateForClient(client)
	state.Lock()
	defer state.Unlock()

	lid := whatsapp.LIDForPN(ctx, targetJID, client)
	indexJIDs := labelChatIndexJIDs(targetJID, lid, labeled)
	patch := buildLabelChatPatch(indexJIDs, request.LabelID, labeled)

	if err = client.SendAppState(ctx, patch); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"chat_jid":   request.ChatJID,
			"index_jids": indexJIDs,
			"label_id":   request.LabelID,
			"label_name": request.LabelName,
			"labeled":    labeled,
		}).Error("Failed to send label chat app state")
		return response, err
	}

	// Optimistic cache update; the resync after SendAppState confirms it.
	now := time.Now()
	for _, jid := range indexJIDs {
		state.ApplyAssociation(jid.String(), request.LabelID, labeled, now)
	}

	// Build response
	response.Status = "success"
	response.ChatJID = targetJID.String()
	if !lid.IsEmpty() {
		response.ChatLID = lid.String()
	}
	response.LabelID = request.LabelID
	response.LabelName = request.LabelName
	response.Labeled = labeled
	response.Message = labelChatMessage(request.LabelName, labeled)

	logrus.WithFields(logrus.Fields{
		"chat_jid":   response.ChatJID,
		"chat_lid":   response.ChatLID,
		"label_id":   request.LabelID,
		"label_name": request.LabelName,
		"labeled":    labeled,
	}).Info("Chat label operation completed successfully")

	return response, nil
}

// labelChatIndexJIDs picks the `label_jid` index keys to write: the LID when
// known (the phone's form) for an apply, both LID and PN for a removal.
func labelChatIndexJIDs(pn, lid types.JID, labeled bool) []types.JID {
	if lid.IsEmpty() {
		return []types.JID{pn}
	}
	if labeled {
		return []types.JID{lid}
	}
	return []types.JID{lid, pn}
}

// buildLabelChatPatch is appstate.BuildLabelChat for several index keys in
// one patch (same collection, one mutation per key).
func buildLabelChatPatch(indexJIDs []types.JID, labelID string, labeled bool) appstate.PatchInfo {
	patch := appstate.PatchInfo{Type: appstate.WAPatchRegular}
	for _, jid := range indexJIDs {
		single := appstate.BuildLabelChat(jid, labelID, labeled)
		patch.Mutations = append(patch.Mutations, single.Mutations...)
	}
	return patch
}

func labelChatMessage(labelName string, labeled bool) string {
	if labeled {
		return fmt.Sprintf("Chat labeled successfully with label '%s'", labelName)
	}
	return fmt.Sprintf("Label '%s' removed from chat successfully", labelName)
}
