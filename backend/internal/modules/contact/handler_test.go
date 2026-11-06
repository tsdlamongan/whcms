package contact_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/contact"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fake middleware bundle

// fakeMW stubs contact.Middlewares: RateLimit is a pass-through that records
// its registration parameters.
type fakeMW struct {
	rateLimited int
	prefix      string
	limit       int
	window      time.Duration
}

func (m *fakeMW) RateLimit(prefix string, limit int, window time.Duration) fiber.Handler {
	m.prefix, m.limit, m.window = prefix, limit, window
	return func(c fiber.Ctx) error {
		m.rateLimited++
		return c.Next()
	}
}

var _ contact.Middlewares = (*fakeMW)(nil)

// Harness

type harness struct {
	app     *fiber.App
	tickets *mocks.MockGuestTicketCreator
	mw      *fakeMW
	captcha *fakeCaptcha
}

func newApp() *harness {
	tickets := &mocks.MockGuestTicketCreator{}
	mw := &fakeMW{}
	captcha := &fakeCaptcha{}
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error { return httpx.Fail(c, err) },
	})
	svc := contact.New(contact.Deps{Tickets: tickets, Clock: &mocks.MockClock{}, Captcha: captcha})
	h := contact.NewHandler(svc, mw)
	h.RegisterRoutes(app.Group("/api/v1"))
	return &harness{app: app, tickets: tickets, mw: mw, captcha: captcha}
}

func (h *harness) post(t *testing.T, body any) (*http.Response, httpx.Envelope) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest("POST", "/api/v1/contact", r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.app.Test(req)
	require.NoError(t, err)
	var env httpx.Envelope
	if resp.StatusCode != fiber.StatusNoContent {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	}
	_ = resp.Body.Close()
	return resp, env
}

// Tests

func TestHandlerSubmitCreated(t *testing.T) {
	h := newApp()
	var got ports.GuestTicketInput
	h.tickets.CreateGuestTicketFn = func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
		got = in
		return &domain.Ticket{ID: 11, TicketNumber: "TKT-000042"}, nil
	}

	resp, env := h.post(t, map[string]any{
		"name":          "Jane Guest",
		"email":         "jane@example.com",
		"subject":       "Cannot access site",
		"message":       "My website is unreachable",
		"department_id": 3,
	})
	assert.Equal(t, 201, resp.StatusCode)
	require.Nil(t, env.Error)
	assert.Equal(t, 1, h.mw.rateLimited, "public route is rate limited")

	// The response exposes ONLY the ticket number (no id/thread).
	raw, _ := json.Marshal(env.Data)
	var res map[string]any
	require.NoError(t, json.Unmarshal(raw, &res))
	assert.Equal(t, "TKT-000042", res["ticket_number"])
	_, hasID := res["id"]
	assert.False(t, hasID, "the id must not leak to the anonymous caller")

	assert.Equal(t, int64(3), got.DepartmentID)
	assert.Equal(t, "Jane Guest", got.GuestName)
}

func TestHandlerRateLimitRegistration(t *testing.T) {
	h := newApp()
	assert.Equal(t, "contact", h.mw.prefix)
	assert.Equal(t, 5, h.mw.limit)
	assert.Equal(t, time.Minute, h.mw.window)
}

func TestHandlerSubmitBindError(t *testing.T) {
	h := newApp()
	called := false
	h.tickets.CreateGuestTicketFn = func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
		called = true
		return &domain.Ticket{}, nil
	}
	req := httptest.NewRequest("POST", "/api/v1/contact", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.app.Test(req)
	require.NoError(t, err)
	var env httpx.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	_ = resp.Body.Close()
	assert.Equal(t, 422, resp.StatusCode)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
	assert.False(t, called)
}

func TestHandlerSubmitValidationError(t *testing.T) {
	h := newApp()
	resp, env := h.post(t, map[string]any{
		"name":    "x", // too short
		"email":   "jane@example.com",
		"subject": "Cannot access site",
		"message": "My website is unreachable",
	})
	assert.Equal(t, 422, resp.StatusCode)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerSubmitCaptchaRejected(t *testing.T) {
	h := newApp()
	h.captcha.err = apperr.Validation("captcha verification required",
		apperr.FieldError{Field: "captcha_token", Message: "is required"})
	called := false
	h.tickets.CreateGuestTicketFn = func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
		called = true
		return &domain.Ticket{}, nil
	}
	resp, env := h.post(t, map[string]any{
		"name":          "Jane Guest",
		"email":         "jane@example.com",
		"subject":       "Cannot access site",
		"message":       "My website is unreachable",
		"captcha_token": "tok-from-widget",
	})
	assert.Equal(t, 422, resp.StatusCode)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
	// The token posted in the body is what reaches the guard.
	assert.Equal(t, "tok-from-widget", h.captcha.lastToken)
	assert.False(t, called, "no ticket must be created when the captcha fails")
}

func TestHandlerSubmitServiceError(t *testing.T) {
	h := newApp()
	h.tickets.CreateGuestTicketFn = func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
		return nil, apperr.Validation("invalid department",
			apperr.FieldError{Field: "department_id", Message: "department not found or inactive"})
	}
	resp, env := h.post(t, map[string]any{
		"name":          "Jane Guest",
		"email":         "jane@example.com",
		"subject":       "Cannot access site",
		"message":       "My website is unreachable",
		"department_id": 99,
	})
	assert.Equal(t, 422, resp.StatusCode)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}
