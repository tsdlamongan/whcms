package auth

import (
	"context"
	"strconv"
	"time"

	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
)

// Public auth endpoints rate limit (per IP; login lockout is enforced in the
// service on top of this).
const (
	publicLimit  = 30
	publicWindow = time.Minute
)

// ServiceAPI is the use-case surface the handler consumes (implemented by
// *Service; mocked in handler tests).
type ServiceAPI interface {
	Register(ctx context.Context, in RegisterRequest) (*AuthResponse, error)
	Login(ctx context.Context, in LoginRequest) (*AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*AuthResponse, error)
	Logout(ctx context.Context, userID int64, refreshToken string) error
	VerifyEmail(ctx context.Context, token string) error
	ResendVerification(ctx context.Context, email string) error
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, in ResetPasswordRequest) error
	Me(ctx context.Context, userID int64) (*MeResponse, error)
	UpdateMe(ctx context.Context, userID int64, in UpdateMeRequest) (*MeResponse, error)
	TwoFASetup(ctx context.Context, userID int64) (*TwoFASetupResponse, error)
	TwoFAEnable(ctx context.Context, userID int64, in TwoFAEnableRequest) error
	TwoFADisable(ctx context.Context, userID int64, in TwoFADisableRequest) error
	ConfirmPassword(ctx context.Context, userID int64, in ConfirmPasswordRequest) (*PasswordConfirmation, error)
	PasswordConfirmation(ctx context.Context, userID int64) (*PasswordConfirmation, error)
	Impersonate(ctx context.Context, actorUserID, clientID int64, ip string) (*AuthResponse, error)
}

// Compile-time check.
var _ ServiceAPI = (*Service)(nil)

// Handler exposes the auth HTTP endpoints.
type Handler struct {
	svc ServiceAPI
	mw  *transporthttp.Middleware
}

// NewHandler builds a Handler.
func NewHandler(svc ServiceAPI, mw *transporthttp.Middleware) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the auth routes on r (the /api/v1 group):
//
//	POST  /auth/register                  public, rate limited
//	POST  /auth/login                     public, rate limited
//	POST  /auth/refresh                   public, rate limited
//	POST  /auth/logout                    auth
//	POST  /auth/verify-email              public, rate limited
//	POST  /auth/resend-verification       public, rate limited
//	POST  /auth/forgot-password           public, rate limited
//	POST  /auth/reset-password            public, rate limited
//	GET   /auth/me                        auth
//	PATCH /auth/me                        auth
//	POST  /auth/2fa/setup                 auth
//	POST  /auth/2fa/enable                auth
//	POST  /auth/2fa/disable               auth
//	POST  /auth/confirm-password          auth
//	GET   /auth/confirm-password          auth
//	POST  /admin/clients/:id/impersonate  auth + RequireRole(admin)
func (h *Handler) RegisterRoutes(r fiber.Router) {
	rl := h.mw.RateLimit("auth", publicLimit, publicWindow)
	auth := r.Group("/auth")

	auth.Post("/register", rl, h.Register)
	auth.Post("/login", rl, h.Login)
	auth.Post("/refresh", rl, h.Refresh)
	auth.Post("/logout", h.mw.RequireAuth(), h.Logout)
	auth.Post("/verify-email", rl, h.VerifyEmail)
	auth.Post("/resend-verification", rl, h.ResendVerification)
	auth.Post("/forgot-password", rl, h.ForgotPassword)
	auth.Post("/reset-password", rl, h.ResetPassword)
	auth.Get("/me", h.mw.RequireAuth(), h.Me)
	auth.Patch("/me", h.mw.RequireAuth(), h.UpdateMe)
	auth.Post("/2fa/setup", h.mw.RequireAuth(), h.TwoFASetup)
	auth.Post("/2fa/enable", h.mw.RequireAuth(), h.TwoFAEnable)
	auth.Post("/2fa/disable", h.mw.RequireAuth(), h.TwoFADisable)
	auth.Post("/confirm-password", h.mw.RequireAuth(), h.ConfirmPassword)
	auth.Get("/confirm-password", h.mw.RequireAuth(), h.PasswordConfirmation)

	r.Post("/admin/clients/:id/impersonate",
		h.mw.RequireAuth(), h.mw.RequireRole("admin"), h.Impersonate)
}

func bind[T any](c fiber.Ctx) (T, error) {
	var dto T
	if err := c.Bind().Body(&dto); err != nil {
		return dto, apperr.Validation("invalid request body")
	}
	return dto, nil
}

// Register handles POST /auth/register.
func (h *Handler) Register(c fiber.Ctx) error {
	in, err := bind[RegisterRequest](c)
	if err != nil {
		return err
	}
	in.IP = c.IP()
	resp, err := h.svc.Register(c.Context(), in)
	if err != nil {
		return err
	}
	return httpx.Created(c, resp)
}

// Login handles POST /auth/login.
func (h *Handler) Login(c fiber.Ctx) error {
	in, err := bind[LoginRequest](c)
	if err != nil {
		return err
	}
	in.IP = c.IP()
	resp, err := h.svc.Login(c.Context(), in)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// Refresh handles POST /auth/refresh.
func (h *Handler) Refresh(c fiber.Ctx) error {
	in, err := bind[RefreshRequest](c)
	if err != nil {
		return err
	}
	resp, err := h.svc.Refresh(c.Context(), in.RefreshToken)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c fiber.Ctx) error {
	// Body is optional: logout without a refresh token is still a 204.
	in, _ := bind[LogoutRequest](c)
	id := httpx.MustIdentity(c)
	if err := h.svc.Logout(c.Context(), id.UserID, in.RefreshToken); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// VerifyEmail handles POST /auth/verify-email.
func (h *Handler) VerifyEmail(c fiber.Ctx) error {
	in, err := bind[VerifyEmailRequest](c)
	if err != nil {
		return err
	}
	if in.Token == "" {
		return apperr.Validation("token required",
			apperr.FieldError{Field: "token", Message: "is required"})
	}
	if err := h.svc.VerifyEmail(c.Context(), in.Token); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"verified": true})
}

// ResendVerification handles POST /auth/resend-verification.
func (h *Handler) ResendVerification(c fiber.Ctx) error {
	in, err := bind[ResendVerificationRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.ResendVerification(c.Context(), in.Email); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{
		"message": "if the account exists and is unverified, a verification email has been sent",
	})
}

// ForgotPassword handles POST /auth/forgot-password (always 200).
func (h *Handler) ForgotPassword(c fiber.Ctx) error {
	in, err := bind[ForgotPasswordRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.ForgotPassword(c.Context(), in.Email); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{
		"message": "if the email exists, a reset link has been sent",
	})
}

// ResetPassword handles POST /auth/reset-password.
func (h *Handler) ResetPassword(c fiber.Ctx) error {
	in, err := bind[ResetPasswordRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.ResetPassword(c.Context(), in); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"reset": true})
}

// Me handles GET /auth/me.
func (h *Handler) Me(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	resp, err := h.svc.Me(c.Context(), id.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// UpdateMe handles PATCH /auth/me.
func (h *Handler) UpdateMe(c fiber.Ctx) error {
	in, err := bind[UpdateMeRequest](c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	resp, err := h.svc.UpdateMe(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// TwoFASetup handles POST /auth/2fa/setup.
func (h *Handler) TwoFASetup(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	resp, err := h.svc.TwoFASetup(c.Context(), id.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// TwoFAEnable handles POST /auth/2fa/enable.
func (h *Handler) TwoFAEnable(c fiber.Ctx) error {
	in, err := bind[TwoFAEnableRequest](c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.TwoFAEnable(c.Context(), id.UserID, in); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"enabled": true})
}

// TwoFADisable handles POST /auth/2fa/disable.
func (h *Handler) TwoFADisable(c fiber.Ctx) error {
	in, err := bind[TwoFADisableRequest](c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.TwoFADisable(c.Context(), id.UserID, in); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"enabled": false})
}

// ConfirmPassword handles POST /auth/confirm-password.
func (h *Handler) ConfirmPassword(c fiber.Ctx) error {
	in, err := bind[ConfirmPasswordRequest](c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	resp, err := h.svc.ConfirmPassword(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// PasswordConfirmation handles GET /auth/confirm-password.
func (h *Handler) PasswordConfirmation(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	resp, err := h.svc.PasswordConfirmation(c.Context(), id.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}

// Impersonate handles POST /admin/clients/:id/impersonate (admin only).
func (h *Handler) Impersonate(c fiber.Ctx) error {
	clientID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || clientID < 1 {
		return apperr.Validation("invalid client id")
	}
	id := httpx.MustIdentity(c)
	resp, err := h.svc.Impersonate(c.Context(), id.UserID, clientID, c.IP())
	if err != nil {
		return err
	}
	return httpx.OK(c, resp)
}
