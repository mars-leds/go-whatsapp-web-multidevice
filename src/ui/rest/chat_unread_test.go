package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	domainChat "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chat"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// unreadChatSpy records the UnreadChat call so the REST handler can be
// exercised without a WhatsApp client.
type unreadChatSpy struct {
	requestChatHistorySpy
	lastRequest domainChat.UnreadChatRequest
	calls       int
}

func (s *unreadChatSpy) UnreadChat(_ context.Context, request domainChat.UnreadChatRequest) (domainChat.UnreadChatResponse, error) {
	s.calls++
	s.lastRequest = request
	unread := request.Unread == nil || *request.Unread
	msg := "Chat marked as read successfully"
	if unread {
		msg = "Chat marked as unread successfully"
	}
	return domainChat.UnreadChatResponse{Status: "success", Message: msg, ChatJID: request.ChatJID, Unread: unread}, nil
}

func newUnreadChatApp(spy *unreadChatSpy) *fiber.App {
	app := fiber.New()
	controller := &Chat{Service: spy}
	app.Post("/chat/:chat_jid/unread", controller.UnreadChat)
	return app
}

func TestUnreadChatAllowsEmptyBody(t *testing.T) {
	spy := &unreadChatSpy{}
	app := newUnreadChatApp(spy)

	req := httptest.NewRequest("POST", "/chat/6289685028129%40s.whatsapp.net/unread", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, 1, spy.calls)
	require.Equal(t, "6289685028129@s.whatsapp.net", spy.lastRequest.ChatJID)
	require.Nil(t, spy.lastRequest.Unread)
	require.Equal(t, "", spy.lastRequest.JIDForm)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var envelope utils.ResponseData
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.Equal(t, "SUCCESS", envelope.Code)
	require.Contains(t, envelope.Message, "unread")
}

func TestUnreadChatBindsBodyAndRouteJID(t *testing.T) {
	spy := &unreadChatSpy{}
	app := newUnreadChatApp(spy)

	body := `{"chat_jid":"other@s.whatsapp.net","unread":false,"jid_form":"both"}`
	req := httptest.NewRequest("POST", "/chat/6289685028129%40s.whatsapp.net/unread", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, 1, spy.calls)
	require.Equal(t, "6289685028129@s.whatsapp.net", spy.lastRequest.ChatJID, "route JID wins over body")
	require.NotNil(t, spy.lastRequest.Unread)
	require.False(t, *spy.lastRequest.Unread)
	require.Equal(t, "both", spy.lastRequest.JIDForm)
}

func TestUnreadChatRejectsMalformedBody(t *testing.T) {
	spy := &unreadChatSpy{}
	app := newUnreadChatApp(spy)

	req := httptest.NewRequest("POST", "/chat/6289685028129%40s.whatsapp.net/unread", strings.NewReader(`{"unread":`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	require.Equal(t, 0, spy.calls)
}
