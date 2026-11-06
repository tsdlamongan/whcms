// Package auth implements the M-AUTH module (FR-AUTH-001..008): registration
// with email verification, login with Argon2id verify + lockout + optional
// TOTP, refresh-token rotation, logout, forgot/reset password, profile self
// service (/auth/me), 2FA setup/enable/disable and admin impersonation.
//
// Route table (see Handler.RegisterRoutes):
//
//	POST  /auth/register                     public (rate limited)
//	POST  /auth/login                        public (rate limited + lockout)
//	POST  /auth/refresh                      public (rate limited)
//	POST  /auth/logout                       auth
//	POST  /auth/verify-email                 public (rate limited)
//	POST  /auth/resend-verification          auth
//	POST  /auth/forgot-password              public (rate limited, always 200)
//	POST  /auth/reset-password               public (rate limited)
//	GET   /auth/me                           auth
//	PATCH /auth/me                           auth
//	POST  /auth/2fa/setup                    auth
//	POST  /auth/2fa/enable                   auth
//	POST  /auth/2fa/disable                  auth
//	POST  /auth/confirm-password             auth (opens the sudo-mode window)
//	GET   /auth/confirm-password             auth (is the window open?)
//	POST  /admin/clients/:id/impersonate     auth + role admin
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/crypto"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/service/authtoken"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Token/lockout policy constants.
const (
	verifyEmailTTL   = 24 * time.Hour
	resetPasswordTTL = time.Hour
	loginMaxAttempts = 5
	loginLockWindow  = 15 * time.Minute
	defaultIssuer    = "WHCMS"
	defaultLocale    = "id"
	// totpReplayTTL bounds how long a login TOTP step is remembered for
	// anti-replay purposes. TOTP steps are 30s wide and the library's default
	// skew accepts ±1 step (~90s total validity), so a few minutes is
	// comfortably longer than any code can remain valid.
	totpReplayTTL = 3 * time.Minute
)

// dummyPasswordHash is a fixed, precomputed-once Argon2id PHC string used to
// pay the same memory-hard verify cost on the "email not found" path as a
// real "wrong password" attempt. Its content is irrelevant (the comparison
// always fails); only its Argon2id parameters must match those produced by
// platform/crypto.PasswordHasher.Hash so the work is genuinely equivalent.
// Without this, Login would return in ~microseconds for unregistered emails
// but ~tens-of-milliseconds for registered ones, letting an attacker
// enumerate valid accounts by timing alone.
const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$+PERzrHeiHQlfHXkZPbhLw$EJuFYjRQvxcp6A+x+KbJYg3mgK1qNKoFqzr/xP2rbs0"

// Email template keys (seeded in 000002_seed_core).
const (
	tplVerifyEmail   = "verify_email"
	tplResetPassword = "reset_password"
)

// TokenManager is the consumer-side surface of authtoken.Manager.
type TokenManager interface {
	IssueAccess(userID int64, role string, clientID int64) (token, jti string, err error)
	CreateRefresh(ctx context.Context, userID int64, jti string) (string, error)
	ConsumeRefresh(ctx context.Context, token string) (userID int64, jti string, err error)
	RevokeRefresh(ctx context.Context, token string) error
	// RevokeAllForUser invalidates every outstanding session of a user
	// (called on password reset/change).
	RevokeAllForUser(ctx context.Context, userID int64) error
}

// Compile-time check: the foundation token core satisfies TokenManager.
var _ TokenManager = (*authtoken.Manager)(nil)

// CaptchaGuard verifies a CAPTCHA token on public actions when CAPTCHA is
// enabled (a no-op returning nil when disabled). Satisfied by
// *service/captcha.Guard. Optional: a nil guard disables the check entirely
// (used by tests and when no verifier is wired).
type CaptchaGuard interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// Deps are the service dependencies (small interfaces only; wiring satisfies
// them from platform/* and repositories).
type Deps struct {
	Users     ports.UserRepo
	Clients   ports.ClientRepo
	Tx        ports.TxManager
	Hasher    ports.PasswordHasher
	Tokens    TokenManager
	OneTime   ports.TokenStore // verify_email / reset_password tokens
	Notify    ports.NotificationSender
	Limiter   ports.RateLimiter
	Audit     ports.AuditLogger
	Clock     ports.Clock
	Encryptor ports.Encryptor       // twofa_secret_enc at-rest encryption
	TOTPGuard ports.TOTPReplayGuard // anti-replay for login TOTP codes
	Presence  ports.PresenceTracker // "Staff Online" heartbeat; nil disables it
	Cache     ports.Cache           // password-confirmation window (confirm.go); nil disables it
	Captcha   CaptchaGuard          // optional CAPTCHA on register/login; nil disables it

	FrontendURL string // e.g. http://localhost:5173 (links in emails)
	TOTPIssuer  string // otpauth issuer; default "WHCMS"

	// TOTP hooks - default to platform/crypto; overridable in tests.
	GenerateTOTP func(issuer, account string) (secret, url string, err error)
	// ValidateTOTP is a plain valid/invalid check, used by the 2FA
	// setup/enable/disable flows where a step-matched replay window doesn't
	// apply (see ValidateTOTPStep for the login path).
	ValidateTOTP func(code, secret string) bool
	// ValidateTOTPStep additionally reports which time-step matched, so Login
	// can reject a step already consumed for the same user (anti-replay).
	ValidateTOTPStep func(code, secret string) (step int64, ok bool)
}

// Service implements the auth use-cases.
type Service struct {
	d Deps
	v *validate.Validator
}

// New builds a Service, filling optional Deps defaults.
func New(d Deps) *Service {
	if d.TOTPIssuer == "" {
		d.TOTPIssuer = defaultIssuer
	}
	if d.GenerateTOTP == nil {
		d.GenerateTOTP = crypto.GenerateTOTPSecret
	}
	if d.ValidateTOTP == nil {
		d.ValidateTOTP = crypto.ValidateTOTP
	}
	if d.ValidateTOTPStep == nil {
		d.ValidateTOTPStep = crypto.ValidateTOTPStep
	}
	return &Service{d: d, v: validate.New()}
}

func isNotFound(err error) bool {
	return errors.Is(err, apperr.New(apperr.CodeNotFound, ""))
}

// normalizeEmail lowercases + trims an email address.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Register creates the user (role client) and client profile in one
// transaction, then best-effort sends the verification email and returns a
// logged-in token pair (FR-AUTH-001). Email must be verified before checkout;
// login itself is not blocked.
func (s *Service) Register(ctx context.Context, in RegisterRequest) (*AuthResponse, error) {
	if err := s.v.Struct(in); err != nil {
		return nil, err
	}
	if s.d.Captcha != nil {
		if err := s.d.Captcha.Verify(ctx, in.CaptchaToken, in.IP); err != nil {
			return nil, err
		}
	}
	email := normalizeEmail(in.Email)

	existing, err := s.d.Users.GetByEmail(ctx, email)
	if err != nil && !isNotFound(err) {
		return nil, apperr.From(err)
	}
	if existing != nil {
		return nil, apperr.Conflict("email already registered")
	}

	hash, err := s.d.Hasher.Hash(in.Password)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	locale := in.Locale
	if locale == "" {
		locale = defaultLocale
	}
	country := in.Country
	if country == "" {
		country = "ID"
	}

	user := &domain.User{
		Email:        email,
		PasswordHash: hash,
		Role:         domain.RoleClient,
		Status:       domain.UserActive,
		Locale:       locale,
	}
	client := &domain.Client{
		FirstName: in.FirstName,
		LastName:  in.LastName,
		Company:   in.Company,
		Address1:  in.Address1,
		Address2:  in.Address2,
		City:      in.City,
		State:     in.State,
		Postcode:  in.Postcode,
		Country:   country,
		Phone:     in.Phone,
		Currency:  "IDR",
		Status:    domain.ClientActive,
	}

	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.d.Users.Create(txCtx, user); err != nil {
			return err
		}
		client.UserID = user.ID
		return s.d.Clients.Create(txCtx, client)
	})
	if err != nil {
		return nil, apperr.From(err)
	}

	s.d.Audit.Log(ctx, user.ID, "auth.register", "user", user.ID, nil, map[string]any{
		"email": user.Email, "role": string(user.Role),
	})

	// Best effort: a failed email must not fail the registration; the user can
	// hit /auth/resend-verification.
	s.sendVerification(ctx, user)

	resp, err := s.issueTokens(ctx, user, client.ID)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// sendVerification creates a verify_email token (24h) and sends the template.
func (s *Service) sendVerification(ctx context.Context, user *domain.User) {
	token, err := s.d.OneTime.Create(ctx, ports.TokenKindVerifyEmail, user.ID, verifyEmailTTL)
	if err != nil {
		return
	}
	_ = s.d.Notify.SendTemplate(ctx, user.ID, tplVerifyEmail, map[string]any{
		"Email":     user.Email,
		"VerifyURL": s.d.FrontendURL + "/verify-email?token=" + token,
	})
}

// Login verifies credentials with lockout (5 attempts / 15 min per IP+email)
// and the TOTP code when 2FA is enabled, updates last_login_at, audits every
// login attempt (success and failure) and returns a token pair on success
// (FR-AUTH-002, FR-AUTH-006).
func (s *Service) Login(ctx context.Context, in LoginRequest) (*AuthResponse, error) {
	if err := s.v.Struct(in); err != nil {
		return nil, err
	}
	if s.d.Captcha != nil {
		if err := s.d.Captcha.Verify(ctx, in.CaptchaToken, in.IP); err != nil {
			return nil, err
		}
	}
	email := normalizeEmail(in.Email)

	// Fixed-window lockout: loginMaxAttempts per loginLockWindow per IP+email.
	// The counter is reset on successful login; the limiter fails open.
	lockKey := "auth:login:" + in.IP + ":" + email
	if allowed, err := s.d.Limiter.Allow(ctx, lockKey, loginMaxAttempts, loginLockWindow); err == nil && !allowed {
		return nil, apperr.New(apperr.CodeRateLimited, "too many login attempts, try again in 15 minutes")
	}

	user, err := s.d.Users.GetByEmail(ctx, email)
	if err != nil || user == nil {
		if err != nil && !isNotFound(err) {
			return nil, apperr.From(err)
		}
		// No such account: still pay the Argon2id verify cost against a fixed
		// dummy hash so this path is not distinguishable-by-timing from a
		// wrong-password attempt on a real account. The result is discarded;
		// the error is always the same generic "invalid credentials".
		_, _ = s.d.Hasher.Verify(in.Password, dummyPasswordHash)
		s.auditLoginFailed(ctx, 0, in.IP, email, "unknown_email")
		return nil, apperr.Unauthorized("invalid email or password")
	}

	ok, err := s.d.Hasher.Verify(in.Password, user.PasswordHash)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !ok {
		s.auditLoginFailed(ctx, user.ID, in.IP, email, "wrong_password")
		return nil, apperr.Unauthorized("invalid email or password")
	}
	if user.Status != domain.UserActive {
		s.auditLoginFailed(ctx, user.ID, in.IP, email, "inactive_account")
		return nil, apperr.Forbidden("account is inactive")
	}

	if user.TwoFAEnabled {
		if in.TOTPCode == "" {
			s.auditLoginFailed(ctx, user.ID, in.IP, email, "totp_required")
			return nil, apperr.Validation("totp_code required",
				apperr.FieldError{Field: "totp_code", Message: "is required"})
		}
		secret, err := s.d.Encryptor.Decrypt(user.TwoFASecretEnc)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		step, valid := s.d.ValidateTOTPStep(in.TOTPCode, secret)
		if !valid {
			s.auditLoginFailed(ctx, user.ID, in.IP, email, "totp_invalid")
			return nil, apperr.Unauthorized("invalid 2FA code")
		}
		// Anti-replay: a code whose matched step was already accepted for
		// this user (or an older step, in case of clock skew) is rejected
		// with the same generic error as a wrong code - the caller must not
		// be able to distinguish "wrong" from "already used".
		accepted, err := s.d.TOTPGuard.Accept(ctx, user.ID, step, totpReplayTTL)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if !accepted {
			s.auditLoginFailed(ctx, user.ID, in.IP, email, "totp_replay")
			return nil, apperr.Unauthorized("invalid 2FA code")
		}
	}

	clientID, err := s.clientIDFor(ctx, user)
	if err != nil {
		return nil, err
	}

	now := s.d.Clock.Now()
	if err := s.d.Users.SetLastLogin(ctx, user.ID, now); err != nil {
		return nil, apperr.From(err)
	}
	user.LastLoginAt = &now

	_ = s.d.Limiter.Reset(ctx, lockKey)
	// A fresh sign-in starts a fresh session: any password confirmation left
	// over from an earlier one (confirm.go) must not carry across.
	s.clearPasswordConfirmation(ctx, user.ID)

	s.d.Audit.Log(ctx, user.ID, "auth.login", "user", user.ID, nil,
		map[string]any{"ip": in.IP, "role": string(user.Role)})

	return s.issueTokens(ctx, user, clientID)
}

// auditLoginFailed records a rejected login attempt. userID is 0 (recorded as
// no actor) when the email doesn't match any account.
func (s *Service) auditLoginFailed(ctx context.Context, userID int64, ip, email, reason string) {
	s.d.Audit.Log(ctx, userID, "auth.login_failed", "user", userID, nil,
		map[string]any{"ip": ip, "email": email, "reason": reason})
}

// Refresh rotates a refresh token: consumes the old one and returns a fresh
// access+refresh pair (FR-AUTH-003).
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*AuthResponse, error) {
	if refreshToken == "" {
		return nil, apperr.Validation("refresh_token required",
			apperr.FieldError{Field: "refresh_token", Message: "is required"})
	}
	userID, _, err := s.d.Tokens.ConsumeRefresh(ctx, refreshToken)
	if err != nil {
		return nil, apperr.From(err)
	}
	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperr.Unauthorized("account no longer exists")
	}
	if user.Status != domain.UserActive {
		return nil, apperr.Forbidden("account is inactive")
	}
	clientID, err := s.clientIDFor(ctx, user)
	if err != nil {
		return nil, err
	}
	return s.issueTokens(ctx, user, clientID)
}

// Logout revokes the given refresh token (no-op when empty/unknown).
func (s *Service) Logout(ctx context.Context, userID int64, refreshToken string) error {
	if s.d.Presence != nil {
		_ = s.d.Presence.Untouch(ctx, userID) // best-effort: never block logout on Redis
	}
	s.clearPasswordConfirmation(ctx, userID)
	s.d.Audit.Log(ctx, userID, "auth.logout", "user", userID, nil, nil)
	if refreshToken == "" {
		return nil
	}
	if err := s.d.Tokens.RevokeRefresh(ctx, refreshToken); err != nil {
		return apperr.From(err)
	}
	return nil
}

// VerifyEmail consumes a verify_email token and marks the user verified
// (FR-AUTH-001).
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	userID, err := s.d.OneTime.Consume(ctx, ports.TokenKindVerifyEmail, token)
	if err != nil {
		if isNotFound(err) {
			return apperr.Validation("invalid or expired verification token")
		}
		return apperr.From(err)
	}
	if err := s.d.Users.SetEmailVerified(ctx, userID, s.d.Clock.Now()); err != nil {
		return apperr.From(err)
	}
	s.d.Audit.Log(ctx, userID, "auth.verify_email", "user", userID, nil, nil)
	return nil
}

// ResendVerification re-sends the verification email for an unverified user
// identified by email. Public, unauthenticated endpoint: never reveals
// whether the account exists or is already verified (always succeeds), same
// posture as ForgotPassword.
func (s *Service) ResendVerification(ctx context.Context, email string) error {
	user, err := s.d.Users.GetByEmail(ctx, normalizeEmail(email))
	if err != nil || user == nil || user.EmailVerifiedAt != nil {
		return nil // always-200: do not leak account existence or verification status
	}
	token, err := s.d.OneTime.Create(ctx, ports.TokenKindVerifyEmail, user.ID, verifyEmailTTL)
	if err != nil {
		return nil
	}
	_ = s.d.Notify.SendTemplate(ctx, user.ID, tplVerifyEmail, map[string]any{
		"Email":     user.Email,
		"VerifyURL": s.d.FrontendURL + "/verify-email?token=" + token,
	})
	s.d.Audit.Log(ctx, user.ID, "auth.resend_verification", "user", user.ID, nil, nil)
	return nil
}

// ForgotPassword issues a reset_password token (1h) and emails the reset link.
// It never reveals whether the email exists (always succeeds) (FR-AUTH-004).
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	user, err := s.d.Users.GetByEmail(ctx, normalizeEmail(email))
	if err != nil || user == nil {
		return nil // always-200: do not leak account existence
	}
	token, err := s.d.OneTime.Create(ctx, ports.TokenKindResetPassword, user.ID, resetPasswordTTL)
	if err != nil {
		return nil
	}
	_ = s.d.Notify.SendTemplate(ctx, user.ID, tplResetPassword, map[string]any{
		"Email":    user.Email,
		"ResetURL": s.d.FrontendURL + "/reset-password?token=" + token,
	})
	s.d.Audit.Log(ctx, user.ID, "auth.forgot_password", "user", user.ID, nil, nil)
	return nil
}

// ResetPassword consumes a reset_password token and sets the new password.
func (s *Service) ResetPassword(ctx context.Context, in ResetPasswordRequest) error {
	if err := s.v.Struct(in); err != nil {
		return err
	}
	userID, err := s.d.OneTime.Consume(ctx, ports.TokenKindResetPassword, in.Token)
	if err != nil {
		if isNotFound(err) {
			return apperr.Validation("invalid or expired reset token")
		}
		return apperr.From(err)
	}
	hash, err := s.d.Hasher.Hash(in.Password)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.d.Users.UpdatePassword(ctx, userID, hash); err != nil {
		return apperr.From(err)
	}
	if err := s.d.Tokens.RevokeAllForUser(ctx, userID); err != nil {
		return apperr.Internal(err)
	}
	s.d.Audit.Log(ctx, userID, "auth.password_reset", "user", userID, nil, nil)
	return nil
}

// Me returns the caller's user DTO plus client profile (nil for staff/admin).
// The frontend calls this on every SSR page load, so it doubles as the
// "Staff Online" heartbeat for admin/staff users (best-effort: a Redis hiccup
// here must never fail the request).
func (s *Service) Me(ctx context.Context, userID int64) (*MeResponse, error) {
	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperr.From(errOrNotFound(err, "user"))
	}
	if s.d.Presence != nil && (user.Role == domain.RoleAdmin || user.Role == domain.RoleStaff) {
		_ = s.d.Presence.Touch(ctx, user.ID, user.Email, string(user.Role))
	}
	client, err := s.clientFor(ctx, user)
	if err != nil {
		return nil, err
	}
	var clientID int64
	if client != nil {
		clientID = client.ID
	}
	return &MeResponse{User: userDTO(user, clientID), Client: client}, nil
}

// UpdateMe updates profile fields (locale on the user; name/company/phone on
// the client profile) and optionally changes the password when NewPassword is
// set (requires the correct CurrentPassword). Email is immutable (FR-AUTH-005).
func (s *Service) UpdateMe(ctx context.Context, userID int64, in UpdateMeRequest) (*MeResponse, error) {
	if err := s.v.Struct(in); err != nil {
		return nil, err
	}
	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperr.From(errOrNotFound(err, "user"))
	}

	if in.NewPassword != "" {
		if in.CurrentPassword == "" {
			return nil, apperr.Validation("current password required",
				apperr.FieldError{Field: "current_password", Message: "is required"})
		}
		ok, err := s.d.Hasher.Verify(in.CurrentPassword, user.PasswordHash)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if !ok {
			return nil, apperr.Unauthorized("current password is incorrect")
		}
		hash, err := s.d.Hasher.Hash(in.NewPassword)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if err := s.d.Users.UpdatePassword(ctx, userID, hash); err != nil {
			return nil, apperr.From(err)
		}
		// Invalidate every session (including other devices); the current
		// access JWT stays valid for its remaining ≤15m lifetime.
		if err := s.d.Tokens.RevokeAllForUser(ctx, userID); err != nil {
			return nil, apperr.Internal(err)
		}
		s.d.Audit.Log(ctx, userID, "auth.password_change", "user", userID, nil, nil)
	}

	if in.Locale != nil && *in.Locale != user.Locale {
		user.Locale = *in.Locale
		if err := s.d.Users.Update(ctx, user); err != nil {
			return nil, apperr.From(err)
		}
	}

	client, err := s.clientFor(ctx, user)
	if err != nil {
		return nil, err
	}
	if client != nil && (in.FirstName != nil || in.LastName != nil || in.Company != nil || in.Phone != nil) {
		if in.FirstName != nil {
			client.FirstName = *in.FirstName
		}
		if in.LastName != nil {
			client.LastName = *in.LastName
		}
		if in.Company != nil {
			client.Company = *in.Company
		}
		if in.Phone != nil {
			client.Phone = *in.Phone
		}
		if err := s.d.Clients.Update(ctx, client); err != nil {
			return nil, apperr.From(err)
		}
	}

	var clientID int64
	if client != nil {
		clientID = client.ID
	}
	return &MeResponse{User: userDTO(user, clientID), Client: client}, nil
}

// TwoFASetup generates a fresh TOTP secret, stores it encrypted (2FA stays
// disabled until enabled with a valid code) and returns the secret + otpauth
// URL (FR-AUTH-006).
func (s *Service) TwoFASetup(ctx context.Context, userID int64) (*TwoFASetupResponse, error) {
	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperr.From(errOrNotFound(err, "user"))
	}
	if user.TwoFAEnabled {
		return nil, apperr.Conflict("2FA is already enabled")
	}
	secret, url, err := s.d.GenerateTOTP(s.d.TOTPIssuer, user.Email)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	enc, err := s.d.Encryptor.Encrypt(secret)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	user.TwoFASecretEnc = enc
	user.TwoFAEnabled = false
	if err := s.d.Users.Update(ctx, user); err != nil {
		return nil, apperr.From(err)
	}
	return &TwoFASetupResponse{Secret: secret, OTPAuthURL: url}, nil
}

// TwoFAEnable turns 2FA on after verifying a code against the stored secret.
func (s *Service) TwoFAEnable(ctx context.Context, userID int64, in TwoFAEnableRequest) error {
	if err := s.v.Struct(in); err != nil {
		return err
	}
	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return apperr.From(errOrNotFound(err, "user"))
	}
	if user.TwoFAEnabled {
		return apperr.Conflict("2FA is already enabled")
	}
	if user.TwoFASecretEnc == "" {
		return apperr.Conflict("2FA setup required first")
	}
	secret, err := s.d.Encryptor.Decrypt(user.TwoFASecretEnc)
	if err != nil {
		return apperr.Internal(err)
	}
	if !s.d.ValidateTOTP(in.Code, secret) {
		return apperr.Unauthorized("invalid 2FA code")
	}
	user.TwoFAEnabled = true
	if err := s.d.Users.Update(ctx, user); err != nil {
		return apperr.From(err)
	}
	s.d.Audit.Log(ctx, userID, "auth.2fa_enable", "user", userID, nil, nil)
	return nil
}

// TwoFADisable turns 2FA off after verifying the password and a valid code.
func (s *Service) TwoFADisable(ctx context.Context, userID int64, in TwoFADisableRequest) error {
	if err := s.v.Struct(in); err != nil {
		return err
	}
	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return apperr.From(errOrNotFound(err, "user"))
	}
	if !user.TwoFAEnabled {
		return apperr.Conflict("2FA is not enabled")
	}
	ok, err := s.d.Hasher.Verify(in.Password, user.PasswordHash)
	if err != nil {
		return apperr.Internal(err)
	}
	if !ok {
		return apperr.Unauthorized("password is incorrect")
	}
	secret, err := s.d.Encryptor.Decrypt(user.TwoFASecretEnc)
	if err != nil {
		return apperr.Internal(err)
	}
	if !s.d.ValidateTOTP(in.Code, secret) {
		return apperr.Unauthorized("invalid 2FA code")
	}
	user.TwoFAEnabled = false
	user.TwoFASecretEnc = ""
	if err := s.d.Users.Update(ctx, user); err != nil {
		return apperr.From(err)
	}
	s.d.Audit.Log(ctx, userID, "auth.2fa_disable", "user", userID, nil, nil)
	return nil
}

// Impersonate (admin only, enforced at the route) issues a token pair for the
// client's user and audit-logs the action (FR-AUTH-008).
func (s *Service) Impersonate(ctx context.Context, actorUserID, clientID int64, ip string) (*AuthResponse, error) {
	client, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil || client == nil {
		return nil, apperr.From(errOrNotFound(err, "client"))
	}
	user, err := s.d.Users.GetByID(ctx, client.UserID)
	if err != nil || user == nil {
		return nil, apperr.From(errOrNotFound(err, "user"))
	}
	if user.Status != domain.UserActive {
		return nil, apperr.Conflict("client user account is inactive")
	}
	resp, err := s.issueTokens(ctx, user, client.ID)
	if err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "auth.impersonate", "client", clientID, nil,
		map[string]any{"user_id": user.ID, "email": user.Email, "ip": ip})
	return resp, nil
}

// clientIDFor resolves the client profile id for client-role users (0 for
// staff/admin or when the profile is missing).
func (s *Service) clientIDFor(ctx context.Context, user *domain.User) (int64, error) {
	client, err := s.clientFor(ctx, user)
	if err != nil {
		return 0, err
	}
	if client == nil {
		return 0, nil
	}
	return client.ID, nil
}

// clientFor loads the client profile for client-role users (nil otherwise).
func (s *Service) clientFor(ctx context.Context, user *domain.User) (*domain.Client, error) {
	if user.Role != domain.RoleClient {
		return nil, nil
	}
	client, err := s.d.Clients.GetByUserID(ctx, user.ID)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, apperr.From(err)
	}
	return client, nil
}

// issueTokens creates the access JWT + refresh token for user.
func (s *Service) issueTokens(ctx context.Context, user *domain.User, clientID int64) (*AuthResponse, error) {
	access, jti, err := s.d.Tokens.IssueAccess(user.ID, string(user.Role), clientID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	refresh, err := s.d.Tokens.CreateRefresh(ctx, user.ID, jti)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &AuthResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         userDTO(user, clientID),
	}, nil
}

// errOrNotFound returns err when non-nil, else a NOT_FOUND for entity (guards
// against repos/mocks returning (nil, nil)).
func errOrNotFound(err error, entity string) error {
	if err != nil {
		return err
	}
	return apperr.NotFound(entity)
}
