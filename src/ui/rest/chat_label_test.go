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

// labelChatSpy records the LabelChat call so the REST handler can be
// exercised without a WhatsApp client. It embeds requestChatHistorySpy for
// the rest of the domainChat.IChatUsecase surface.
type labelChatSpy struct {
	requestChatHistorySpy
	lastRequest domainChat.LabelChatRequest
	calls       int
}

func (s *labelChatSpy) LabelChat(_ context.Context, request domainChat.LabelChatRequest) (domainChat.LabelChatResponse, error) {
	s.calls++
	s.lastRequest = request
	return domainChat.LabelChatResponse{
		Status:    "success",
		Message:   "Chat labeled successfully with label '" + request.LabelName + "'",
		ChatJID:   request.ChatJID,
		LabelID:   request.LabelID,
		LabelName: request.LabelName,
		Labeled:   request.Labeled != nil && *request.Labeled,
	}, nil
}

func newLabelChatApp(spy *labelChatSpy) *fiber.App {
	app := fiber.New()
	controller := &Chat{Service: spy}
	app.Post("/chat/:chat_jid/label", controller.LabelChat)
	return app
}

func TestLabelChatBindsBodyAndRouteJID(t *testing.T) {
	spy := &labelChatSpy{}
	app := newLabelChatApp(spy)

	// A body chat_jid must never override the route parameter.
	body := `{"chat_jid":"other@s.whatsapp.net","label_id":"7","label_name":"Important","labeled":true}`
	req := httptest.NewRequest("POST", "/chat/6289685028129%40s.whatsapp.net/label", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, 1, spy.calls)
	require.Equal(t, "6289685028129@s.whatsapp.net", spy.lastRequest.ChatJID)
	require.Equal(t, "7", spy.lastRequest.LabelID)
	require.Equal(t, "Important", spy.lastRequest.LabelName)
	require.NotNil(t, spy.lastRequest.Labeled)
	require.True(t, *spy.lastRequest.Labeled)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var envelope utils.ResponseData
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.Equal(t, "SUCCESS", envelope.Code)
	require.Contains(t, envelope.Message, "Important")
}

func TestLabelChatKeepsMissingLabeledNil(t *testing.T) {
	spy := &labelChatSpy{}
	app := newLabelChatApp(spy)

	// Without `labeled` the pointer must stay nil so validation can reject
	// it instead of silently unlabeling the chat.
	body := `{"label_id":"7","label_name":"Important"}`
	req := httptest.NewRequest("POST", "/chat/6289685028129%40s.whatsapp.net/label", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, 1, spy.calls)
	require.Nil(t, spy.lastRequest.Labeled)
}

func TestLabelChatRejectsMalformedBody(t *testing.T) {
	spy := &labelChatSpy{}
	app := newLabelChatApp(spy)

	req := httptest.NewRequest("POST", "/chat/6289685028129%40s.whatsapp.net/label", strings.NewReader(`{"label_id":`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	require.Equal(t, 0, spy.calls)
}
