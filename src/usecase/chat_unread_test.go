package usecase

import (
	"context"
	"database/sql"
	"testing"
	"time"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	domainChatStorage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chatstorage"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/chatstorage"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

func TestUnreadChatIndexJIDs(t *testing.T) {
	pn := types.NewJID("5491112345678", types.DefaultUserServer)
	lid := types.NewJID("223754944819424", "lid")

	assert.Equal(t, []types.JID{pn}, unreadChatIndexJIDs(pn, types.EmptyJID, domainChat.UnreadJIDFormPN))
	assert.Equal(t, []types.JID{pn}, unreadChatIndexJIDs(pn, types.EmptyJID, domainChat.UnreadJIDFormLID), "no LID known: number")
	assert.Equal(t, []types.JID{pn}, unreadChatIndexJIDs(pn, types.EmptyJID, domainChat.UnreadJIDFormBoth))
	assert.Equal(t, []types.JID{pn}, unreadChatIndexJIDs(pn, lid, domainChat.UnreadJIDFormPN))
	assert.Equal(t, []types.JID{lid}, unreadChatIndexJIDs(pn, lid, domainChat.UnreadJIDFormLID))
	assert.Equal(t, []types.JID{lid, pn}, unreadChatIndexJIDs(pn, lid, domainChat.UnreadJIDFormBoth))
}

// TestMarkChatAsReadPatchShape pins the whatsmeow patch this usecase relies
// on: collection `regular_low`, index [markChatAsRead, <chat_jid>], version
// 3, a MarkChatAsReadAction with `read` and a message range whose last
// timestamp is in seconds and whose single message carries the key.
func TestMarkChatAsReadPatchShape(t *testing.T) {
	pn := types.NewJID("5491112345678", types.DefaultUserServer)
	lid := types.NewJID("223754944819424", "lid")
	ts := time.Unix(1_700_000_000, 0)
	key := lastMessageKey(&domainChatStorage.Message{ID: "ABC", ChatJID: pn.String(), IsFromMe: true})

	ref := appstate.BuildMarkChatAsRead(pn, false, ts, key)
	patch := buildMarkChatAsReadPatch([]types.JID{lid, pn}, false, ts, key)

	assert.Equal(t, appstate.WAPatchRegularLow, patch.Type)
	assert.Equal(t, ref.Type, patch.Type)
	require.Len(t, patch.Mutations, 2)
	assert.Equal(t, []string{appstate.IndexMarkChatAsRead, lid.String()}, patch.Mutations[0].Index)
	assert.Equal(t, []string{appstate.IndexMarkChatAsRead, pn.String()}, patch.Mutations[1].Index)
	assert.Equal(t, ref.Mutations[0].Version, patch.Mutations[1].Version)
	assert.Equal(t, int32(3), patch.Mutations[1].Version)

	for _, m := range patch.Mutations {
		action := m.Value.GetMarkChatAsReadAction()
		require.NotNil(t, action)
		assert.False(t, action.GetRead())
		rng := action.GetMessageRange()
		require.NotNil(t, rng)
		assert.Equal(t, ts.Unix(), rng.GetLastMessageTimestamp())
		require.Len(t, rng.GetMessages(), 1)
		assert.Equal(t, "ABC", rng.GetMessages()[0].GetKey().GetID())
		assert.True(t, rng.GetMessages()[0].GetKey().GetFromMe())
		assert.Equal(t, pn.String(), rng.GetMessages()[0].GetKey().GetRemoteJID())
		assert.Equal(t, ts.Unix(), rng.GetMessages()[0].GetTimestamp())
	}

	// No stored message: range is "now", no key.
	before := time.Now().Unix()
	empty := buildMarkChatAsReadPatch([]types.JID{pn}, true, time.Time{}, nil)
	rng := empty.Mutations[0].Value.GetMarkChatAsReadAction().GetMessageRange()
	assert.GreaterOrEqual(t, rng.GetLastMessageTimestamp(), before)
	assert.Empty(t, rng.GetMessages())
	assert.True(t, empty.Mutations[0].Value.GetMarkChatAsReadAction().GetRead())
}

func TestLastMessageKeyGroupParticipant(t *testing.T) {
	group := "120363000000000000@g.us"
	theirs := lastMessageKey(&domainChatStorage.Message{ID: "G1", ChatJID: group, IsFromMe: false, Sender: "628987654321@s.whatsapp.net"})
	assert.Equal(t, "628987654321@s.whatsapp.net", theirs.GetParticipant())
	assert.False(t, theirs.GetFromMe())

	mine := lastMessageKey(&domainChatStorage.Message{ID: "G2", ChatJID: group, IsFromMe: true, Sender: "me@s.whatsapp.net"})
	assert.Nil(t, mine.Participant, "own group messages carry no participant")

	direct := lastMessageKey(&domainChatStorage.Message{ID: "D1", ChatJID: "5491112345678@s.whatsapp.net", Sender: "5491112345678@s.whatsapp.net"})
	assert.Nil(t, direct.Participant, "1:1 chats carry no participant")
}

func newUnreadTestRepo(t *testing.T) domainChatStorage.IChatStorageRepository {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repo := chatstorage.NewStorageRepository(db)
	require.NoError(t, repo.InitializeSchema())
	return repo
}

func TestLastMessageRange(t *testing.T) {
	repo := newUnreadTestRepo(t)
	deviceID := "device-a@s.whatsapp.net"
	pn := types.NewJID("5491112345678", types.DefaultUserServer)
	lid := types.NewJID("223754944819424", "lid")
	t0 := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)

	require.NoError(t, repo.StoreChat(&domainChatStorage.Chat{DeviceID: deviceID, JID: pn.String(), Name: "x", LastMessageTime: t0}))
	require.NoError(t, repo.StoreMessage(&domainChatStorage.Message{ID: "OLD", ChatJID: pn.String(), DeviceID: deviceID, Sender: pn.String(), Content: "old", Timestamp: t0}))
	require.NoError(t, repo.StoreMessage(&domainChatStorage.Message{ID: "NEW", ChatJID: pn.String(), DeviceID: deviceID, Sender: deviceID, Content: "new", Timestamp: t0.Add(time.Minute), IsFromMe: true}))
	// Another device's message must not leak in.
	require.NoError(t, repo.StoreChat(&domainChatStorage.Chat{DeviceID: "device-b@s.whatsapp.net", JID: pn.String(), Name: "x", LastMessageTime: t0}))
	require.NoError(t, repo.StoreMessage(&domainChatStorage.Message{ID: "OTHER", ChatJID: pn.String(), DeviceID: "device-b@s.whatsapp.net", Sender: pn.String(), Content: "b", Timestamp: t0.Add(time.Hour)}))

	service := serviceChat{chatStorageRepo: repo}
	ts, key, id := service.lastMessageRange(deviceID, pn, lid)
	assert.True(t, ts.Equal(t0.Add(time.Minute)), "newest message wins")
	assert.Equal(t, "NEW", id)
	require.NotNil(t, key)
	assert.Equal(t, "NEW", key.GetID())
	assert.True(t, key.GetFromMe())
	assert.Equal(t, pn.String(), key.GetRemoteJID())

	// Chat stored under its LID only.
	require.NoError(t, repo.StoreChat(&domainChatStorage.Chat{DeviceID: deviceID, JID: lid.String(), Name: "y", LastMessageTime: t0}))
	require.NoError(t, repo.StoreMessage(&domainChatStorage.Message{ID: "L1", ChatJID: lid.String(), DeviceID: deviceID, Sender: lid.String(), Content: "l", Timestamp: t0}))
	other := types.NewJID("5491199999999", types.DefaultUserServer)
	_, _, id = service.lastMessageRange(deviceID, other, lid)
	assert.Equal(t, "L1", id, "falls back to the LID form")

	// Unknown chat, no storage, no device.
	_, key, id = service.lastMessageRange(deviceID, other, types.EmptyJID)
	assert.Nil(t, key)
	assert.Equal(t, "", id)
	_, key, _ = serviceChat{}.lastMessageRange(deviceID, pn, lid)
	assert.Nil(t, key)
	_, key, _ = service.lastMessageRange("", pn, lid)
	assert.Nil(t, key)
}

func TestUnreadChatRequiresClient(t *testing.T) {
	service := NewChatService(newUnreadTestRepo(t))
	ctx := context.Background()

	_, err := service.UnreadChat(ctx, domainChat.UnreadChatRequest{})
	assert.Equal(t, pkgError.ValidationError("chat_jid: cannot be blank."), err)
	_, err = service.UnreadChat(ctx, domainChat.UnreadChatRequest{ChatJID: "5491112345678@s.whatsapp.net", JIDForm: "nope"})
	assert.Equal(t, pkgError.ValidationError("jid_form: must be a valid value."), err)
	_, err = service.UnreadChat(ctx, domainChat.UnreadChatRequest{ChatJID: "5491112345678@s.whatsapp.net"})
	assert.Equal(t, pkgError.ErrWaCLI, err)
}
