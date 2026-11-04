package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	domainLabel "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/label"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// labelSpy records the usecase calls so the REST handlers can be exercised
// without a WhatsApp client.
type labelSpy struct {
	listReq   domainLabel.ListLabelsRequest
	createReq domainLabel.CreateLabelRequest
	updateReq domainLabel.UpdateLabelRequest
	deleteReq domainLabel.DeleteLabelRequest
	calls     map[string]int
}

func newLabelSpy() *labelSpy { return &labelSpy{calls: map[string]int{}} }

func (s *labelSpy) ListLabels(_ context.Context, request domainLabel.ListLabelsRequest) (domainLabel.ListLabelsResponse, error) {
	s.calls["list"]++
	s.listReq = request
	return domainLabel.ListLabelsResponse{
		Labels:    []domainLabel.Label{{LabelID: "13", Name: "ATENCION HUMANA", Color: 4, IsActive: true, Type: "CUSTOM"}},
		Chats:     []domainLabel.ChatLabel{{ChatID: "5491112345678@s.whatsapp.net", ChatLID: "223754944819424@lid", LabelID: "13", Labeled: true}},
		Refreshed: request.Refresh,
	}, nil
}

func (s *labelSpy) CreateLabel(_ context.Context, request domainLabel.CreateLabelRequest) (domainLabel.CreateLabelResponse, error) {
	s.calls["create"]++
	s.createReq = request
	return domainLabel.CreateLabelResponse{Status: "success", Message: "Label '" + request.Name + "' created with id 16", Label: domainLabel.Label{LabelID: "16", Name: request.Name}}, nil
}

func (s *labelSpy) UpdateLabel(_ context.Context, request domainLabel.UpdateLabelRequest) (domainLabel.UpdateLabelResponse, error) {
	s.calls["update"]++
	s.updateReq = request
	return domainLabel.UpdateLabelResponse{Status: "success", Message: "Label " + request.LabelID + " updated"}, nil
}

func (s *labelSpy) DeleteLabel(_ context.Context, request domainLabel.DeleteLabelRequest) (domainLabel.DeleteLabelResponse, error) {
	s.calls["delete"]++
	s.deleteReq = request
	return domainLabel.DeleteLabelResponse{Status: "success", Message: "Label (" + request.LabelID + ") deleted"}, nil
}

func newLabelApp(spy *labelSpy) *fiber.App {
	app := fiber.New()
	InitRestLabel(app, spy)
	return app
}

func doLabelJSON(t *testing.T, app *fiber.App, method, path, body string) (int, utils.ResponseData) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var envelope utils.ResponseData
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	return resp.StatusCode, envelope
}

func TestListLabelsPassesRefreshFlag(t *testing.T) {
	spy := newLabelSpy()
	app := newLabelApp(spy)

	status, envelope := doLabelJSON(t, app, "GET", "/labels", "")
	require.Equal(t, fiber.StatusOK, status)
	require.Equal(t, "SUCCESS", envelope.Code)
	require.False(t, spy.listReq.Refresh)

	results, ok := envelope.Results.(map[string]any)
	require.True(t, ok)
	require.Contains(t, results, "labels")
	require.Contains(t, results, "chats")
	chats := results["chats"].([]any)
	require.Len(t, chats, 1)
	require.Equal(t, "223754944819424@lid", chats[0].(map[string]any)["chat_lid"])

	status, _ = doLabelJSON(t, app, "GET", "/labels?refresh=true", "")
	require.Equal(t, fiber.StatusOK, status)
	require.True(t, spy.listReq.Refresh)
	require.Equal(t, 2, spy.calls["list"])
}

func TestCreateLabelBindsBody(t *testing.T) {
	spy := newLabelSpy()
	app := newLabelApp(spy)

	status, envelope := doLabelJSON(t, app, "POST", "/labels", `{"name":"IRRITADO","color":7}`)
	require.Equal(t, fiber.StatusOK, status)
	require.Equal(t, "SUCCESS", envelope.Code)
	require.Contains(t, envelope.Message, "IRRITADO")
	require.Equal(t, "IRRITADO", spy.createReq.Name)
	require.NotNil(t, spy.createReq.Color)
	require.Equal(t, int32(7), *spy.createReq.Color)

	// color omitted stays nil (usecase defaults it)
	_, _ = doLabelJSON(t, app, "POST", "/labels", `{"name":"X"}`)
	require.Nil(t, spy.createReq.Color)
}

func TestCreateLabelRejectsMalformedBody(t *testing.T) {
	spy := newLabelSpy()
	app := newLabelApp(spy)

	status, envelope := doLabelJSON(t, app, "POST", "/labels", `{"name":`)
	require.Equal(t, fiber.StatusBadRequest, status)
	require.Equal(t, "BAD_REQUEST", envelope.Code)
	require.Equal(t, 0, spy.calls["create"])
}

func TestUpdateLabelRouteIDIsAuthoritative(t *testing.T) {
	spy := newLabelSpy()
	app := newLabelApp(spy)

	status, envelope := doLabelJSON(t, app, "PUT", "/labels/13", `{"label_id":"99","name":"ATENCION HUMANA"}`)
	require.Equal(t, fiber.StatusOK, status)
	require.Equal(t, "SUCCESS", envelope.Code)
	require.Equal(t, "13", spy.updateReq.LabelID)
	require.NotNil(t, spy.updateReq.Name)
	require.Equal(t, "ATENCION HUMANA", *spy.updateReq.Name)
	require.Nil(t, spy.updateReq.Color, "an omitted color must stay nil so the usecase keeps the current one")

	_, _ = doLabelJSON(t, app, "PUT", "/labels/13", `{"color":0}`)
	require.Nil(t, spy.updateReq.Name)
	require.NotNil(t, spy.updateReq.Color)
	require.Equal(t, int32(0), *spy.updateReq.Color, "an explicit 0 must be distinguishable from omitted")

	status, _ = doLabelJSON(t, app, "PUT", "/labels/13", `{"name":`)
	require.Equal(t, fiber.StatusBadRequest, status)
}

func TestDeleteLabelUsesRouteID(t *testing.T) {
	spy := newLabelSpy()
	app := newLabelApp(spy)

	status, envelope := doLabelJSON(t, app, "DELETE", "/labels/13", "")
	require.Equal(t, fiber.StatusOK, status)
	require.Equal(t, "SUCCESS", envelope.Code)
	require.Equal(t, "13", spy.deleteReq.LabelID)
	require.Equal(t, 1, spy.calls["delete"])
}
