package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/modules/auth"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/internal/service/authtoken"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakes

// fakeParser maps bearer tokens to identities.
type fakeParser struct{}

func (fakeParser) ParseAccess(token string) (*authtoken.AccessClaims, error) {
	switch token {
	case "admin-token":
		return &authtoken.AccessClaims{UserID: 1, Role: "admin"}, nil
	case "staff-token":
		return &authtoken.AccessClaims{UserID: 3, Role: "staff"}, nil
	case "client-token":
		return &authtoken.AccessClaims{UserID: 10, Role: "client", ClientID: 77}, nil
	}
	return nil, apperr.Unauthorized("invalid token")
}

// fakeSvc implements auth.ServiceAPI with function fields.
type fakeSvc struct {
	RegisterFn           func(ctx context.Context, in auth.RegisterRequest) (*auth.AuthResponse, error)
	LoginFn              func(ctx context.Context, in auth.LoginRequest) (*auth.AuthResponse, error)
	RefreshFn            func(ctx context.Context, token string) (*auth.AuthResponse, error)
	LogoutFn             func(ctx context.Context, userID int64, token string) error
	VerifyEmailFn        func(ctx context.Context, token string) error
	ResendVerificationFn func(ctx context.Context, email string) error
	ForgotPasswordFn     func(ctx context.Context, email string) error
	ResetPasswordFn      func(ctx context.Context, in auth.ResetPasswordRequest) error
	MeFn                 func(ctx context.Context, userID int64) (*auth.MeResponse, error)
	UpdateMeFn           func(ctx context.Context, userID int64, in auth.UpdateMeRequest) (*auth.MeResponse, error)
	TwoFASetupFn         func(ctx context.Context, userID int64) (*auth.TwoFASetupResponse, error)
	TwoFAEnableFn        func(ctx context.Context, userID int64, in auth.TwoFAEnableRequest) error
	TwoFADisableFn       func(ctx context.Context, userID int64, in auth.TwoFADisableRequest) error
	ConfirmPasswordFn    func(ctx context.Context, userID int64, in auth.ConfirmPasswordRequest) (*auth.PasswordConfirmation, error)
	PasswordConfirmFn    func(ctx context.Context, userID int64) (*auth.PasswordConfirmation, error)
	ImpersonateFn        func(ctx context.Context, actorUserID, clientID int64, ip string) (*auth.AuthResponse, error)
}

func okAuth() *auth.AuthResponse {
	return &auth.AuthResponse{AccessToken: "a", RefreshToken: "r", User: auth.UserDTO{ID: 10}}
}

func (f *fakeSvc) Register(ctx context.Context, in auth.RegisterRequest) (*auth.AuthResponse, error) {
	if f.RegisterFn != nil {
		return f.RegisterFn(ctx, in)
	}
	return okAuth(), nil
}

func (f *fakeSvc) Login(ctx context.Context, in auth.LoginRequest) (*auth.AuthResponse, error) {
	if f.LoginFn != nil {
		return f.LoginFn(ctx, in)
	}
	return okAuth(), nil
}

func (f *fakeSvc) Refresh(ctx context.Context, token string) (*auth.AuthResponse, error) {
	if f.RefreshFn != nil {
		return f.RefreshFn(ctx, token)
	}
	return okAuth(), nil
}

func (f *fakeSvc) Logout(ctx context.Context, userID int64, token string) error {
	if f.LogoutFn != nil {
		return f.LogoutFn(ctx, userID, token)
	}
	return nil
}

func (f *fakeSvc) VerifyEmail(ctx context.Context, token string) error {
	if f.VerifyEmailFn != nil {
		return f.VerifyEmailFn(ctx, token)
	}
	return nil
}

func (f *fakeSvc) ResendVerification(ctx context.Context, email string) error {
	if f.ResendVerificationFn != nil {
		return f.ResendVerificationFn(ctx, email)
	}
	return nil
}

func (f *fakeSvc) ForgotPassword(ctx context.Context, email string) error {
	if f.ForgotPasswordFn != nil {
		return f.ForgotPasswordFn(ctx, email)
	}
	return nil
}

func (f *fakeSvc) ResetPassword(ctx context.Context, in auth.ResetPasswordRequest) error {
	if f.ResetPasswordFn != nil {
		return f.ResetPasswordFn(ctx, in)
	}
	return nil
}

func (f *fakeSvc) Me(ctx context.Context, userID int64) (*auth.MeResponse, error) {
	if f.MeFn != nil {
		return f.MeFn(ctx, userID)
	}
	return &auth.MeResponse{User: auth.UserDTO{ID: userID}}, nil
}

func (f *fakeSvc) UpdateMe(ctx context.Context, userID int64, in auth.UpdateMeRequest) (*auth.MeResponse, error) {
	if f.UpdateMeFn != nil {
		return f.UpdateMeFn(ctx, userID, in)
	}
	return &auth.MeResponse{User: auth.UserDTO{ID: userID}}, nil
}

func (f *fakeSvc) TwoFASetup(ctx context.Context, userID int64) (*auth.TwoFASetupResponse, error) {
	if f.TwoFASetupFn != nil {
		return f.TwoFASetupFn(ctx, userID)
	}
	return &auth.TwoFASetupResponse{Secret: "S", OTPAuthURL: "otpauth://x"}, nil
}

func (f *fakeSvc) TwoFAEnable(ctx context.Context, userID int64, in auth.TwoFAEnableRequest) error {
	if f.TwoFAEnableFn != nil {
		return f.TwoFAEnableFn(ctx, userID, in)
	}
	return nil
}

func (f *fakeSvc) TwoFADisable(ctx context.Context, userID int64, in auth.TwoFADisableRequest) error {
	if f.TwoFADisableFn != nil {
		return f.TwoFADisableFn(ctx, userID, in)
	}
	return nil
}

func (f *fakeSvc) ConfirmPassword(ctx context.Context, userID int64, in auth.ConfirmPasswordRequest) (*auth.PasswordConfirmation, error) {
	if f.ConfirmPasswordFn != nil {
		return f.ConfirmPasswordFn(ctx, userID, in)
	}
	return &auth.PasswordConfirmation{Confirmed: true}, nil
}

func (f *fakeSvc) PasswordConfirmation(ctx context.Context, userID int64) (*auth.PasswordConfirmation, error) {
	if f.PasswordConfirmFn != nil {
		return f.PasswordConfirmFn(ctx, userID)
	}
	return &auth.PasswordConfirmation{}, nil
}

func (f *fakeSvc) Impersonate(ctx context.Context, actorUserID, clientID int64, ip string) (*auth.AuthResponse, error) {
	if f.ImpersonateFn != nil {
		return f.ImpersonateFn(ctx, actorUserID, clientID, ip)
	}
	return okAuth(), nil
}

var _ auth.ServiceAPI = (*fakeSvc)(nil)

// helpers

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newApp(svc auth.ServiceAPI, limiter *mocks.MockRateLimiter) *fiber.App {
	if limiter == nil {
		limiter = &mocks.MockRateLimiter{} // allow-all default
	}
	mw := &transporthttp.Middleware{
		Tokens:  fakeParser{},
		Perms:   transporthttp.PermissionSourceFunc(func(fiber.Ctx, int64, string) (bool, error) { return true, nil }),
		Limiter: limiter,
		Log:     discard,
	}
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discard)})
	auth.NewHandler(svc, mw).RegisterRoutes(app.Group("/api/v1"))
	return app
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func doJSON(t *testing.T, app *fiber.App, method, path, body, bearer string) (int, envelope) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var env envelope
	if resp.StatusCode != fiber.StatusNoContent {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	}
	return resp.StatusCode, env
}

// tests

func TestHandlerRegister(t *testing.T) {
	var got auth.RegisterRequest
	svc := &fakeSvc{RegisterFn: func(_ context.Context, in auth.RegisterRequest) (*auth.AuthResponse, error) {
		got = in
		return okAuth(), nil
	}}
	app := newApp(svc, nil)

	status, env := doJSON(t, app, "POST", "/api/v1/auth/register",
		`{"email":"a@b.co","password":"password1","first_name":"A","last_name":"B","locale":"en"}`, "")
	assert.Equal(t, 201, status)
	assert.Nil(t, env.Error)
	assert.Equal(t, "a@b.co", got.Email)
	assert.Equal(t, "en", got.Locale)
}

func TestHandlerRegisterBadJSON(t *testing.T) {
	app := newApp(&fakeSvc{}, nil)
	status, env := doJSON(t, app, "POST", "/api/v1/auth/register", "{not json", "")
	assert.Equal(t, 422, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerLoginPassesIP(t *testing.T) {
	var got auth.LoginRequest
	svc := &fakeSvc{LoginFn: func(_ context.Context, in auth.LoginRequest) (*auth.AuthResponse, error) {
		got = in
		return okAuth(), nil
	}}
	app := newApp(svc, nil)

	status, _ := doJSON(t, app, "POST", "/api/v1/auth/login",
		`{"email":"a@b.co","password":"secret","ip":"6.6.6.6"}`, "")
	assert.Equal(t, 200, status)
	assert.NotEqual(t, "6.6.6.6", got.IP, "IP must come from the connection, not the body")
	assert.NotEmpty(t, got.IP)
}

func TestHandlerLoginServiceError(t *testing.T) {
	svc := &fakeSvc{LoginFn: func(context.Context, auth.LoginRequest) (*auth.AuthResponse, error) {
		return nil, apperr.Unauthorized("invalid email or password")
	}}
	app := newApp(svc, nil)
	status, env := doJSON(t, app, "POST", "/api/v1/auth/login",
		`{"email":"a@b.co","password":"bad"}`, "")
	assert.Equal(t, 401, status)
	assert.Equal(t, "UNAUTHORIZED", env.Error.Code)
}

func TestHandlerPublicRateLimit(t *testing.T) {
	limiter := &mocks.MockRateLimiter{
		AllowFn: func(context.Context, string, int, time.Duration) (bool, error) { return false, nil },
	}
	app := newApp(&fakeSvc{}, limiter)
	status, env := doJSON(t, app, "POST", "/api/v1/auth/login",
		`{"email":"a@b.co","password":"x"}`, "")
	assert.Equal(t, 429, status)
	assert.Equal(t, "RATE_LIMITED", env.Error.Code)
}

func TestHandlerRefresh(t *testing.T) {
	var got string
	svc := &fakeSvc{RefreshFn: func(_ context.Context, token string) (*auth.AuthResponse, error) {
		got = token
		return okAuth(), nil
	}}
	app := newApp(svc, nil)
	status, _ := doJSON(t, app, "POST", "/api/v1/auth/refresh", `{"refresh_token":"rt-1"}`, "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "rt-1", got)
}

func TestHandlerLogout(t *testing.T) {
	var got string
	var gotUserID int64
	svc := &fakeSvc{LogoutFn: func(_ context.Context, userID int64, token string) error {
		gotUserID, got = userID, token
		return nil
	}}
	app := newApp(svc, nil)

	status, _ := doJSON(t, app, "POST", "/api/v1/auth/logout", `{"refresh_token":"rt-9"}`, "client-token")
	assert.Equal(t, 204, status)
	assert.Equal(t, "rt-9", got)
	assert.Equal(t, int64(10), gotUserID, "identity from the access token, not the request body")

	status, _ = doJSON(t, app, "POST", "/api/v1/auth/logout", `{}`, "")
	assert.Equal(t, 401, status, "logout requires auth")
}

func TestHandlerVerifyEmail(t *testing.T) {
	var got string
	svc := &fakeSvc{VerifyEmailFn: func(_ context.Context, token string) error { got = token; return nil }}
	app := newApp(svc, nil)

	status, _ := doJSON(t, app, "POST", "/api/v1/auth/verify-email", `{"token":"tok"}`, "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "tok", got)

	status, env := doJSON(t, app, "POST", "/api/v1/auth/verify-email", `{}`, "")
	assert.Equal(t, 422, status)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerResendVerification(t *testing.T) {
	var got string
	svc := &fakeSvc{ResendVerificationFn: func(_ context.Context, email string) error {
		got = email
		return nil
	}}
	app := newApp(svc, nil)
	// Public endpoint: no bearer token needed (pre-login, right after register).
	status, _ := doJSON(t, app, "POST", "/api/v1/auth/resend-verification", `{"email":"a@b.co"}`, "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "a@b.co", got)
}

func TestHandlerResendVerificationMalformedJSON(t *testing.T) {
	app := newApp(&fakeSvc{}, nil)
	status, env := doJSON(t, app, "POST", "/api/v1/auth/resend-verification", `{bad`, "")
	assert.Equal(t, 422, status)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerForgotAndResetPassword(t *testing.T) {
	var gotEmail string
	var gotReset auth.ResetPasswordRequest
	svc := &fakeSvc{
		ForgotPasswordFn: func(_ context.Context, email string) error { gotEmail = email; return nil },
		ResetPasswordFn:  func(_ context.Context, in auth.ResetPasswordRequest) error { gotReset = in; return nil },
	}
	app := newApp(svc, nil)

	status, _ := doJSON(t, app, "POST", "/api/v1/auth/forgot-password", `{"email":"a@b.co"}`, "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "a@b.co", gotEmail)

	status, _ = doJSON(t, app, "POST", "/api/v1/auth/reset-password",
		`{"token":"tok","password":"newpassword1"}`, "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "tok", gotReset.Token)
	assert.Equal(t, "newpassword1", gotReset.Password)
}

func TestHandlerMe(t *testing.T) {
	app := newApp(&fakeSvc{}, nil)

	status, env := doJSON(t, app, "GET", "/api/v1/auth/me", "", "client-token")
	assert.Equal(t, 200, status)
	assert.Nil(t, env.Error)

	status, _ = doJSON(t, app, "GET", "/api/v1/auth/me", "", "")
	assert.Equal(t, 401, status)
}

func TestHandlerUpdateMe(t *testing.T) {
	var gotUser int64
	var gotIn auth.UpdateMeRequest
	svc := &fakeSvc{UpdateMeFn: func(_ context.Context, userID int64, in auth.UpdateMeRequest) (*auth.MeResponse, error) {
		gotUser, gotIn = userID, in
		return &auth.MeResponse{User: auth.UserDTO{ID: userID}}, nil
	}}
	app := newApp(svc, nil)

	status, _ := doJSON(t, app, "PATCH", "/api/v1/auth/me",
		`{"locale":"en","current_password":"old","new_password":"newpassword1"}`, "client-token")
	assert.Equal(t, 200, status)
	assert.Equal(t, int64(10), gotUser)
	require.NotNil(t, gotIn.Locale)
	assert.Equal(t, "en", *gotIn.Locale)
	assert.Equal(t, "newpassword1", gotIn.NewPassword)
}

func TestHandlerTwoFA(t *testing.T) {
	var enabled auth.TwoFAEnableRequest
	var disabled auth.TwoFADisableRequest
	svc := &fakeSvc{
		TwoFAEnableFn:  func(_ context.Context, _ int64, in auth.TwoFAEnableRequest) error { enabled = in; return nil },
		TwoFADisableFn: func(_ context.Context, _ int64, in auth.TwoFADisableRequest) error { disabled = in; return nil },
	}
	app := newApp(svc, nil)

	status, env := doJSON(t, app, "POST", "/api/v1/auth/2fa/setup", "", "client-token")
	assert.Equal(t, 200, status)
	assert.Contains(t, string(env.Data), "otpauth")

	status, _ = doJSON(t, app, "POST", "/api/v1/auth/2fa/enable", `{"code":"123456"}`, "client-token")
	assert.Equal(t, 200, status)
	assert.Equal(t, "123456", enabled.Code)

	status, _ = doJSON(t, app, "POST", "/api/v1/auth/2fa/disable",
		`{"password":"pw","code":"123456"}`, "client-token")
	assert.Equal(t, 200, status)
	assert.Equal(t, "pw", disabled.Password)

	status, _ = doJSON(t, app, "POST", "/api/v1/auth/2fa/setup", "", "")
	assert.Equal(t, 401, status)
}

func TestHandlerConfirmPassword(t *testing.T) {
	var gotUser int64
	var gotIn auth.ConfirmPasswordRequest
	svc := &fakeSvc{
		ConfirmPasswordFn: func(_ context.Context, userID int64, in auth.ConfirmPasswordRequest) (*auth.PasswordConfirmation, error) {
			gotUser, gotIn = userID, in
			if in.Password != "right" {
				return nil, apperr.Unauthorized("password is incorrect")
			}
			return &auth.PasswordConfirmation{Confirmed: true}, nil
		},
		PasswordConfirmFn: func(_ context.Context, userID int64) (*auth.PasswordConfirmation, error) {
			gotUser = userID
			return &auth.PasswordConfirmation{Confirmed: true}, nil
		},
	}
	app := newApp(svc, nil)

	status, env := doJSON(t, app, "POST", "/api/v1/auth/confirm-password", `{"password":"right"}`, "admin-token")
	assert.Equal(t, 200, status)
	assert.Equal(t, int64(1), gotUser)
	assert.Equal(t, "right", gotIn.Password)
	assert.JSONEq(t, `{"confirmed":true}`, string(env.Data))

	status, env = doJSON(t, app, "POST", "/api/v1/auth/confirm-password", `{"password":"nope"}`, "admin-token")
	assert.Equal(t, 401, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "UNAUTHORIZED", env.Error.Code)

	status, _ = doJSON(t, app, "POST", "/api/v1/auth/confirm-password", `{not json`, "admin-token")
	assert.Equal(t, 422, status)

	gotUser = 0
	status, env = doJSON(t, app, "GET", "/api/v1/auth/confirm-password", "", "staff-token")
	assert.Equal(t, 200, status)
	assert.Equal(t, int64(3), gotUser)
	assert.JSONEq(t, `{"confirmed":true}`, string(env.Data))

	status, _ = doJSON(t, app, "GET", "/api/v1/auth/confirm-password", "", "")
	assert.Equal(t, 401, status)
	status, _ = doJSON(t, app, "POST", "/api/v1/auth/confirm-password", `{"password":"x"}`, "")
	assert.Equal(t, 401, status)
}

func TestHandlerConfirmPasswordStatusError(t *testing.T) {
	svc := &fakeSvc{PasswordConfirmFn: func(context.Context, int64) (*auth.PasswordConfirmation, error) {
		return nil, apperr.Internal(assert.AnError)
	}}
	status, _ := doJSON(t, newApp(svc, nil), "GET", "/api/v1/auth/confirm-password", "", "admin-token")
	assert.Equal(t, 500, status)
}

func TestHandlerImpersonate(t *testing.T) {
	var gotActor, gotClient int64
	svc := &fakeSvc{ImpersonateFn: func(_ context.Context, actorUserID, clientID int64, _ string) (*auth.AuthResponse, error) {
		gotActor, gotClient = actorUserID, clientID
		return okAuth(), nil
	}}
	app := newApp(svc, nil)

	status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/55/impersonate", "", "admin-token")
	assert.Equal(t, 200, status)
	assert.Equal(t, int64(1), gotActor)
	assert.Equal(t, int64(55), gotClient)
}

func TestHandlerImpersonateForbiddenForNonAdmin(t *testing.T) {
	app := newApp(&fakeSvc{}, nil)

	status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/55/impersonate", "", "client-token")
	assert.Equal(t, 403, status)

	status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/55/impersonate", "", "staff-token")
	assert.Equal(t, 403, status, "impersonation is admin-only, staff excluded")

	status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/55/impersonate", "", "")
	assert.Equal(t, 401, status)
}

func TestHandlerImpersonateBadID(t *testing.T) {
	app := newApp(&fakeSvc{}, nil)
	status, env := doJSON(t, app, "POST", "/api/v1/admin/clients/abc/impersonate", "", "admin-token")
	assert.Equal(t, 422, status)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}
