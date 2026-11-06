package auth

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Password re-confirmation ("sudo mode") for sensitive admin pages such as
// Administrators. The user re-types their password; on success a short-lived
// flag is stored server-side (Redis, keyed by user id) so the BFF can ask
// "has this user confirmed recently?" on every load/action instead of
// trusting anything the browser says.
const (
	passwordConfirmTTL         = 15 * time.Minute
	passwordConfirmMaxAttempts = 5
	passwordConfirmLockWindow  = 15 * time.Minute
)

func passwordConfirmKey(userID int64) string {
	return "auth:pwconfirm:" + strconv.FormatInt(userID, 10)
}

func passwordConfirmAttemptsKey(userID int64) string {
	return "auth:pwconfirm:attempts:" + strconv.FormatInt(userID, 10)
}

// ConfirmPasswordRequest is the POST /auth/confirm-password body.
type ConfirmPasswordRequest struct {
	Password string `json:"password" validate:"required"`
}

// PasswordConfirmation reports whether the caller has a live confirmation.
// ExpiresAt is only set when Confirmed is true.
type PasswordConfirmation struct {
	Confirmed bool       `json:"confirmed"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// passwordConfirmRecord is the value kept in the cache for a live confirmation.
type passwordConfirmRecord struct {
	ExpiresAt time.Time `json:"expires_at"`
}

var errConfirmNotConfigured = errors.New("auth: password confirmation cache is not configured")

// ConfirmPassword verifies the caller's current password and opens a
// passwordConfirmTTL confirmation window. Wrong attempts are limited to
// passwordConfirmMaxAttempts per passwordConfirmLockWindow per user (the
// limiter fails open on Redis errors, like Login's lockout).
func (s *Service) ConfirmPassword(ctx context.Context, userID int64, in ConfirmPasswordRequest) (*PasswordConfirmation, error) {
	if err := s.v.Struct(in); err != nil {
		return nil, err
	}
	if s.d.Cache == nil {
		return nil, apperr.Internal(errConfirmNotConfigured)
	}

	attemptsKey := passwordConfirmAttemptsKey(userID)
	if allowed, err := s.d.Limiter.Allow(ctx, attemptsKey, passwordConfirmMaxAttempts, passwordConfirmLockWindow); err == nil && !allowed {
		return nil, apperr.New(apperr.CodeRateLimited, "too many attempts, try again in 15 minutes")
	}

	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperr.From(errOrNotFound(err, "user"))
	}
	ok, err := s.d.Hasher.Verify(in.Password, user.PasswordHash)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !ok {
		s.d.Audit.Log(ctx, userID, "auth.password_confirm_failed", "user", userID, nil, nil)
		return nil, apperr.Unauthorized("password is incorrect")
	}

	expires := s.d.Clock.Now().Add(passwordConfirmTTL)
	if err := s.d.Cache.SetJSON(ctx, passwordConfirmKey(userID), passwordConfirmRecord{ExpiresAt: expires}, passwordConfirmTTL); err != nil {
		return nil, apperr.Internal(err)
	}
	_ = s.d.Limiter.Reset(ctx, attemptsKey) // best-effort: a success clears the failure counter
	s.d.Audit.Log(ctx, userID, "auth.password_confirm", "user", userID, nil, nil)
	return &PasswordConfirmation{Confirmed: true, ExpiresAt: &expires}, nil
}

// PasswordConfirmation reports the caller's current confirmation state
// (GET /auth/confirm-password). A cache miss or an already-elapsed record
// both mean "not confirmed".
func (s *Service) PasswordConfirmation(ctx context.Context, userID int64) (*PasswordConfirmation, error) {
	if s.d.Cache == nil {
		return nil, apperr.Internal(errConfirmNotConfigured)
	}
	var rec passwordConfirmRecord
	found, err := s.d.Cache.GetJSON(ctx, passwordConfirmKey(userID), &rec)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !found || !rec.ExpiresAt.After(s.d.Clock.Now()) {
		return &PasswordConfirmation{Confirmed: false}, nil
	}
	return &PasswordConfirmation{Confirmed: true, ExpiresAt: &rec.ExpiresAt}, nil
}

// clearPasswordConfirmation drops a live confirmation (best-effort; called on
// logout so the next login has to confirm again).
func (s *Service) clearPasswordConfirmation(ctx context.Context, userID int64) {
	if s.d.Cache == nil {
		return
	}
	_ = s.d.Cache.Delete(ctx, passwordConfirmKey(userID))
}
