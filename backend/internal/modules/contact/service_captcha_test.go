package contact_test

import (
	"context"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/contact"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCaptcha implements contact.CaptchaGuard.
type fakeCaptcha struct {
	err       error
	calls     int
	lastToken string
	lastIP    string
}

func (f *fakeCaptcha) Verify(_ context.Context, token, ip string) error {
	f.calls++
	f.lastToken, f.lastIP = token, ip
	return f.err
}

var _ contact.CaptchaGuard = (*fakeCaptcha)(nil)

func TestSubmitCaptchaRejected(t *testing.T) {
	e := newFixture()
	called := false
	e.tickets.CreateGuestTicketFn = func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
		called = true
		return &domain.Ticket{}, nil
	}
	cap := &fakeCaptcha{err: apperr.Validation("captcha verification failed")}
	svc := contact.New(contact.Deps{Tickets: e.tickets, Captcha: cap})

	in := validInput()
	in.CaptchaToken = "bad"
	_, err := svc.Submit(context.Background(), in, "203.0.113.7")

	requireCode(t, err, apperr.CodeValidation)
	assert.Equal(t, 1, cap.calls)
	assert.Equal(t, "bad", cap.lastToken)
	assert.Equal(t, "203.0.113.7", cap.lastIP)
	assert.False(t, called, "no ticket must be created when the captcha fails")
}

func TestSubmitCaptchaPassthrough(t *testing.T) {
	e := newFixture()
	e.tickets.CreateGuestTicketFn = func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
		return &domain.Ticket{TicketNumber: "TKT-000042"}, nil
	}
	cap := &fakeCaptcha{}
	svc := contact.New(contact.Deps{Tickets: e.tickets, Captcha: cap})

	in := validInput()
	in.CaptchaToken = "ok"
	ticket, err := svc.Submit(context.Background(), in, "203.0.113.7")

	require.NoError(t, err)
	assert.Equal(t, "TKT-000042", ticket.TicketNumber)
	assert.Equal(t, 1, cap.calls)
	assert.Equal(t, "ok", cap.lastToken)
	assert.Equal(t, "203.0.113.7", cap.lastIP)
}

func TestSubmitInvalidInputSkipsCaptcha(t *testing.T) {
	// Garbage input is rejected before the provider is asked, so it never
	// spends a siteverify call.
	cap := &fakeCaptcha{}
	svc := contact.New(contact.Deps{Tickets: newFixture().tickets, Captcha: cap})

	in := validInput()
	in.Email = "not-an-email"
	_, err := svc.Submit(context.Background(), in, "")

	requireCode(t, err, apperr.CodeValidation)
	assert.Zero(t, cap.calls)
}
