// Package contact implements the public "contact us" form: an unauthenticated
// visitor submits a message which is turned into a guest support ticket by
// delegating to the tickets module via ports.GuestTicketCreator. There is no
// repository - the module owns no tables of its own.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	POST /contact    open a guest support ticket (rate limited: 5/min, CAPTCHA when enabled)   [public]
package contact

import (
	"context"
	"errors"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// CaptchaGuard verifies a CAPTCHA token on the contact form when CAPTCHA is
// enabled (a no-op returning nil when disabled). Satisfied by
// *service/captcha.Guard. Optional: a nil guard disables the check entirely.
type CaptchaGuard interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// Deps are the service dependencies (wired by the composition root).
type Deps struct {
	Tickets ports.GuestTicketCreator
	Clock   ports.Clock
	Captcha CaptchaGuard
}

// Service implements the contact use-case.
type Service struct {
	d   Deps
	val *validate.Validator
}

// New builds the contact Service.
func New(d Deps) *Service {
	return &Service{d: d, val: validate.New()}
}

// wrap passes *apperr.Error through and wraps anything else as INTERNAL.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	return apperr.Internal(err)
}

// Submit validates the contact form, verifies the CAPTCHA (when enabled) and
// opens a guest support ticket. ip is the caller's remote address, passed to
// the CAPTCHA provider and recorded on the guest ticket for abuse tracing.
func (s *Service) Submit(ctx context.Context, in ContactInput, ip string) (*domain.Ticket, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if s.d.Captcha != nil {
		if err := s.d.Captcha.Verify(ctx, in.CaptchaToken, ip); err != nil {
			return nil, err
		}
	}
	ticket, err := s.d.Tickets.CreateGuestTicket(ctx, ports.GuestTicketInput{
		DepartmentID: in.DepartmentID,
		GuestName:    in.Name,
		GuestEmail:   in.Email,
		Subject:      in.Subject,
		Message:      in.Message,
		IP:           ip,
	})
	if err != nil {
		return nil, wrap(err)
	}
	return ticket, nil
}
