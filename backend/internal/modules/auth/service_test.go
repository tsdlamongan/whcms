package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/auth"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// test harness

type issuedAccess struct {
	UserID   int64
	Role     string
	ClientID int64
}

// fakeTokens implements auth.TokenManager.
type fakeTokens struct {
	issueErr   error
	createErr  error
	revokeErr  error
	consumeID  int64
	consumeJTI string
	consumeErr error

	issued       []issuedAccess
	created      []string
	revoked      []string
	consumed     []string
	revokedAll   []int64
	revokeAllErr error
	seq          int
}

func (f *fakeTokens) IssueAccess(userID int64, role string, clientID int64) (string, string, error) {
	if f.issueErr != nil {
		return "", "", f.issueErr
	}
	f.seq++
	f.issued = append(f.issued, issuedAccess{userID, role, clientID})
	return fmt.Sprintf("access-%d", f.seq), fmt.Sprintf("jti-%d", f.seq), nil
}

func (f *fakeTokens) CreateRefresh(_ context.Context, userID int64, jti string) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	tok := fmt.Sprintf("refresh-%d-%s", userID, jti)
	f.created = append(f.created, tok)
	return tok, nil
}

func (f *fakeTokens) ConsumeRefresh(_ context.Context, token string) (int64, string, error) {
	if f.consumeErr != nil {
		return 0, "", f.consumeErr
	}
	f.consumed = append(f.consumed, token)
	return f.consumeID, f.consumeJTI, nil
}

func (f *fakeTokens) RevokeRefresh(_ context.Context, token string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revoked = append(f.revoked, token)
	return nil
}

func (f *fakeTokens) RevokeAllForUser(_ context.Context, userID int64) error {
	if f.revokeAllErr != nil {
		return f.revokeAllErr
	}
	f.revokedAll = append(f.revokedAll, userID)
	return nil
}

type sentTemplate struct {
	UserID int64
	Key    string
	Data   map[string]any
}

type env struct {
	users     *mocks.MockUserRepo
	clients   *mocks.MockClientRepo
	tx        *mocks.MockTxManager
	hasher    *mocks.MockPasswordHasher
	tokens    *fakeTokens
	onetime   *mocks.MockTokenStore
	notify    *mocks.MockNotificationSender
	limiter   *mocks.MockRateLimiter
	audit     *mocks.MockAuditLogger
	clock     *mocks.MockClock
	enc       *mocks.MockEncryptor
	totpGuard *mocks.MockTOTPReplayGuard
	presence  *mocks.MockPresenceTracker
	captcha   auth.CaptchaGuard // nil by default -> CAPTCHA check skipped

	sent []sentTemplate
	now  time.Time
}

func newFixture() *env {
	e := &env{
		users:     &mocks.MockUserRepo{},
		clients:   &mocks.MockClientRepo{},
		tx:        &mocks.MockTxManager{},
		hasher:    &mocks.MockPasswordHasher{},
		tokens:    &fakeTokens{},
		onetime:   &mocks.MockTokenStore{},
		notify:    &mocks.MockNotificationSender{},
		limiter:   &mocks.MockRateLimiter{},
		audit:     &mocks.MockAuditLogger{},
		enc:       &mocks.MockEncryptor{},
		totpGuard: &mocks.MockTOTPReplayGuard{},
		presence:  &mocks.MockPresenceTracker{},
		now:       time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
	}
	e.clock = &mocks.MockClock{FixedTime: e.now}
	e.notify.SendTemplateFn = func(_ context.Context, userID int64, key string, data map[string]any) error {
		e.sent = append(e.sent, sentTemplate{UserID: userID, Key: key, Data: data})
		return nil
	}
	e.onetime.CreateFn = func(_ context.Context, kind string, userID int64, _ time.Duration) (string, error) {
		return "tok-" + kind, nil
	}
	return e
}

func (e *env) service() *auth.Service {
	return auth.New(auth.Deps{
		Users:       e.users,
		Clients:     e.clients,
		Tx:          e.tx,
		Hasher:      e.hasher,
		Tokens:      e.tokens,
		OneTime:     e.onetime,
		Notify:      e.notify,
		Limiter:     e.limiter,
		Audit:       e.audit,
		Clock:       e.clock,
		Encryptor:   e.enc,
		TOTPGuard:   e.totpGuard,
		Presence:    e.presence,
		Captcha:     e.captcha,
		FrontendURL: "http://fe",
		TOTPIssuer:  "TestPanel",
		GenerateTOTP: func(issuer, account string) (string, string, error) {
			return "SECRET", "otpauth://totp/" + issuer + ":" + account, nil
		},
		ValidateTOTP: func(code, secret string) bool {
			return code == "123456" && secret == "SECRET"
		},
		ValidateTOTPStep: func(code, secret string) (int64, bool) {
			if code == "123456" && secret == "SECRET" {
				return 1, true
			}
			return 0, false
		},
	})
}

// activeUser returns a client-role user whose password is "correct-password"
// under the MockPasswordHasher default scheme.
func activeUser() *domain.User {
	return &domain.User{
		ID:           10,
		Email:        "user@example.com",
		PasswordHash: "hashed:correct-password",
		Role:         domain.RoleClient,
		Status:       domain.UserActive,
		Locale:       "id",
	}
}

func withUser(e *env, u *domain.User) {
	e.users.GetByEmailFn = func(_ context.Context, email string) (*domain.User, error) {
		if strings.EqualFold(email, u.Email) {
			return u, nil
		}
		return nil, apperr.NotFound("user")
	}
	e.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		if id == u.ID {
			return u, nil
		}
		return nil, apperr.NotFound("user")
	}
}

func withClient(e *env, c *domain.Client) {
	e.clients.GetByUserIDFn = func(_ context.Context, userID int64) (*domain.Client, error) {
		if userID == c.UserID {
			return c, nil
		}
		return nil, apperr.NotFound("client")
	}
	e.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		if id == c.ID {
			return c, nil
		}
		return nil, apperr.NotFound("client")
	}
}

func assertCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	var e *apperr.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, code, e.Code)
}

var ctx = context.Background()

// Register

func validRegister() auth.RegisterRequest {
	return auth.RegisterRequest{
		Email:     "New.User@Example.com",
		Password:  "supersecret1",
		FirstName: "Budi",
		LastName:  "Santoso",
		Phone:     "081234567890",
		Address1:  "Jl. Merdeka No. 1",
		City:      "Jakarta",
		State:     "DKI Jakarta",
		Postcode:  "10110",
	}
}

func TestRegisterSuccess(t *testing.T) {
	e := newFixture()
	var createdUser *domain.User
	var createdClient *domain.Client
	e.users.CreateFn = func(_ context.Context, u *domain.User) error {
		u.ID = 10
		createdUser = u
		return nil
	}
	e.clients.CreateFn = func(_ context.Context, c *domain.Client) error {
		c.ID = 7
		createdClient = c
		return nil
	}

	resp, err := e.service().Register(ctx, validRegister())
	require.NoError(t, err)

	// User row: role client, active, normalized email, default locale.
	require.NotNil(t, createdUser)
	assert.Equal(t, "new.user@example.com", createdUser.Email)
	assert.Equal(t, domain.RoleClient, createdUser.Role)
	assert.Equal(t, domain.UserActive, createdUser.Status)
	assert.Equal(t, "id", createdUser.Locale)
	assert.Equal(t, "hashed:supersecret1", createdUser.PasswordHash)

	// Client row linked to the user, default country/currency.
	require.NotNil(t, createdClient)
	assert.Equal(t, int64(10), createdClient.UserID)
	assert.Equal(t, "Budi", createdClient.FirstName)
	assert.Equal(t, "ID", createdClient.Country)
	assert.Equal(t, "IDR", createdClient.Currency)

	// Verification email sent with the token link.
	require.Len(t, e.sent, 1)
	assert.Equal(t, "verify_email", e.sent[0].Key)
	assert.Equal(t, "http://fe/verify-email?token=tok-verify_email", e.sent[0].Data["VerifyURL"])

	// Auto-login response.
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, int64(7), resp.User.ClientID)
	assert.False(t, resp.User.EmailVerified)
	require.Len(t, e.tokens.issued, 1)
	assert.Equal(t, issuedAccess{10, "client", 7}, e.tokens.issued[0])

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.register", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestRegisterValidation(t *testing.T) {
	cases := map[string]func(*auth.RegisterRequest){
		"bad email":      func(r *auth.RegisterRequest) { r.Email = "not-an-email" },
		"short password": func(r *auth.RegisterRequest) { r.Password = "short" },
		"bad locale":     func(r *auth.RegisterRequest) { r.Locale = "fr" },
		"no first name":  func(r *auth.RegisterRequest) { r.FirstName = "" },
		"bad country":    func(r *auth.RegisterRequest) { r.Country = "IDN" },
		"no address1":    func(r *auth.RegisterRequest) { r.Address1 = "" },
		"no city":        func(r *auth.RegisterRequest) { r.City = "" },
		"no state":       func(r *auth.RegisterRequest) { r.State = "" },
		"no postcode":    func(r *auth.RegisterRequest) { r.Postcode = "" },
		"no phone":       func(r *auth.RegisterRequest) { r.Phone = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newFixture()
			in := validRegister()
			mutate(&in)
			_, err := e.service().Register(ctx, in)
			assertCode(t, err, apperr.CodeValidation)
		})
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	in := validRegister()
	in.Email = "user@example.com"
	_, err := e.service().Register(ctx, in)
	assertCode(t, err, apperr.CodeConflict)
}

func TestRegisterExplicitLocaleAndCountry(t *testing.T) {
	e := newFixture()
	var createdUser *domain.User
	var createdClient *domain.Client
	e.users.CreateFn = func(_ context.Context, u *domain.User) error { createdUser = u; return nil }
	e.clients.CreateFn = func(_ context.Context, c *domain.Client) error { createdClient = c; return nil }

	in := validRegister()
	in.Locale = "en"
	in.Country = "SG"
	_, err := e.service().Register(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "en", createdUser.Locale)
	assert.Equal(t, "SG", createdClient.Country)
}

func TestRegisterVerificationFailureDoesNotFail(t *testing.T) {
	e := newFixture()
	e.onetime.CreateFn = func(context.Context, string, int64, time.Duration) (string, error) {
		return "", errors.New("redis down")
	}
	_, err := e.service().Register(ctx, validRegister())
	require.NoError(t, err)
	assert.Empty(t, e.sent, "no email without a token")
}

func TestRegisterTxFailure(t *testing.T) {
	e := newFixture()
	e.users.CreateFn = func(context.Context, *domain.User) error {
		return apperr.Conflict("email already registered")
	}
	_, err := e.service().Register(ctx, validRegister())
	assertCode(t, err, apperr.CodeConflict)
}

// Login

func login(email, password string) auth.LoginRequest {
	return auth.LoginRequest{Email: email, Password: password, IP: "1.2.3.4"}
}

func TestLoginSuccess(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	withClient(e, &domain.Client{ID: 77, UserID: u.ID})

	var lastLoginID int64
	var lastLoginAt time.Time
	e.users.SetLastLoginFn = func(_ context.Context, id int64, at time.Time) error {
		lastLoginID, lastLoginAt = id, at
		return nil
	}
	var resetKey string
	e.limiter.ResetFn = func(_ context.Context, key string) error { resetKey = key; return nil }

	resp, err := e.service().Login(ctx, login("USER@example.com", "correct-password"))
	require.NoError(t, err)

	assert.Equal(t, int64(10), lastLoginID)
	assert.Equal(t, e.now, lastLoginAt)
	assert.Equal(t, "auth:login:1.2.3.4:user@example.com", resetKey)
	require.Len(t, e.tokens.issued, 1)
	assert.Equal(t, issuedAccess{10, "client", 77}, e.tokens.issued[0])
	assert.Equal(t, int64(77), resp.User.ClientID)

	require.Len(t, e.audit.Entries, 1, "every successful login is audited, client role included")
	assert.Equal(t, "auth.login", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestLoginWrongPassword(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	_, err := e.service().Login(ctx, login("user@example.com", "wrong"))
	assertCode(t, err, apperr.CodeUnauthorized)
	assert.Empty(t, e.tokens.issued)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login_failed", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestLoginUnknownEmail(t *testing.T) {
	e := newFixture()
	e.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
		return nil, apperr.NotFound("user")
	}
	_, err := e.service().Login(ctx, login("nobody@example.com", "whatever1"))
	assertCode(t, err, apperr.CodeUnauthorized)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login_failed", e.audit.Entries[0].Action)
	assert.Equal(t, int64(0), e.audit.Entries[0].ActorUserID, "no known account: recorded with no actor")
}

// TestLoginUnknownEmailPaysHashingCost is a regression test for a timing
// side-channel: previously, Login returned as soon as GetByEmail reported
// "not found" without ever invoking the Argon2id hasher, while a
// wrong-password attempt on a real account always called Hasher.Verify. That
// asymmetry let an attacker enumerate registered emails by measuring
// response latency alone. The fix must call Hasher.Verify exactly once on
// the unknown-email path too (against a fixed dummy hash), matching the
// wrong-password path's call count. We assert call counts via a
// MockPasswordHasher wrapper rather than wall-clock timing, since timing
// assertions are inherently flaky.
func TestLoginUnknownEmailPaysHashingCost(t *testing.T) {
	e := newFixture()
	e.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
		return nil, apperr.NotFound("user")
	}
	var verifyCalls int
	var lastPHC string
	e.hasher.VerifyFn = func(password, phc string) (bool, error) {
		verifyCalls++
		lastPHC = phc
		return false, nil
	}

	_, err := e.service().Login(ctx, login("nobody@example.com", "whatever1"))
	assertCode(t, err, apperr.CodeUnauthorized)
	assert.Equal(t, 1, verifyCalls, "unknown-email login must still invoke Hasher.Verify exactly once")
	assert.Contains(t, lastPHC, "$argon2id$", "dummy hash must be a well-formed argon2id PHC string")

	// Same call count as a real account with a wrong password, so the two
	// paths are not distinguishable by hasher invocation (and therefore not
	// by the memory-hard work it performs).
	e2 := newFixture()
	withUser(e2, activeUser())
	var wrongPasswordVerifyCalls int
	e2.hasher.VerifyFn = func(password, phc string) (bool, error) {
		wrongPasswordVerifyCalls++
		return false, nil
	}
	_, err = e2.service().Login(ctx, login("user@example.com", "wrong"))
	assertCode(t, err, apperr.CodeUnauthorized)
	assert.Equal(t, wrongPasswordVerifyCalls, verifyCalls, "unknown-email and wrong-password paths must call Hasher.Verify the same number of times")
}

func TestLoginInactiveAccount(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.Status = domain.UserInactive
	withUser(e, u)
	_, err := e.service().Login(ctx, login("user@example.com", "correct-password"))
	assertCode(t, err, apperr.CodeForbidden)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login_failed", e.audit.Entries[0].Action)
}

func TestLoginLockout(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	var gotKey string
	var gotLimit int
	var gotWindow time.Duration
	e.limiter.AllowFn = func(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
		gotKey, gotLimit, gotWindow = key, limit, window
		return false, nil
	}
	_, err := e.service().Login(ctx, login("user@example.com", "correct-password"))
	assertCode(t, err, apperr.CodeRateLimited)
	assert.Equal(t, "auth:login:1.2.3.4:user@example.com", gotKey)
	assert.Equal(t, 5, gotLimit)
	assert.Equal(t, 15*time.Minute, gotWindow)
}

func TestLoginLimiterFailsOpen(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	e.limiter.AllowFn = func(context.Context, string, int, time.Duration) (bool, error) {
		return false, errors.New("redis down")
	}
	_, err := e.service().Login(ctx, login("user@example.com", "correct-password"))
	require.NoError(t, err)
}

func TestLoginTOTPRequired(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET" // MockEncryptor passes through
	withUser(e, u)

	_, err := e.service().Login(ctx, login("user@example.com", "correct-password"))
	assertCode(t, err, apperr.CodeValidation)
	var appErr *apperr.Error
	require.ErrorAs(t, err, &appErr)
	require.Len(t, appErr.Details, 1)
	assert.Equal(t, "totp_code", appErr.Details[0].Field)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login_failed", e.audit.Entries[0].Action)
}

func TestLoginTOTPWrongCode(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)

	in := login("user@example.com", "correct-password")
	in.TOTPCode = "000000"
	_, err := e.service().Login(ctx, in)
	assertCode(t, err, apperr.CodeUnauthorized)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login_failed", e.audit.Entries[0].Action)
}

func TestLoginTOTPSuccess(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)

	in := login("user@example.com", "correct-password")
	in.TOTPCode = "123456"
	resp, err := e.service().Login(ctx, in)
	require.NoError(t, err)
	assert.True(t, resp.User.TwoFAEnabled)
}

// TestLoginTOTPPassesMatchedStepToGuard is a regression test for anti-replay
// wiring: Login must derive the matched step via ValidateTOTPStep and hand it
// to TOTPGuard.Accept (keyed by user id) rather than skipping replay checks.
func TestLoginTOTPPassesMatchedStepToGuard(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)

	var gotUserID, gotStep int64
	var gotTTL time.Duration
	e.totpGuard.AcceptFn = func(_ context.Context, userID int64, step int64, ttl time.Duration) (bool, error) {
		gotUserID, gotStep, gotTTL = userID, step, ttl
		return true, nil
	}

	in := login("user@example.com", "correct-password")
	in.TOTPCode = "123456"
	_, err := e.service().Login(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, u.ID, gotUserID)
	assert.Equal(t, int64(1), gotStep, "matched step from ValidateTOTPStep must reach the guard")
	assert.Equal(t, 3*time.Minute, gotTTL)
}

// TestLoginTOTPReplayRejected is a regression test for CVE-class TOTP replay:
// a code whose matched step was already accepted for this user (per
// TOTPGuard) must be rejected even though ValidateTOTPStep says it's a
// currently-valid code - and with the SAME generic error as a wrong code, so
// the response doesn't leak "already used" vs "wrong" to the caller.
func TestLoginTOTPReplayRejected(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)

	e.totpGuard.AcceptFn = func(context.Context, int64, int64, time.Duration) (bool, error) {
		return false, nil // simulate: this step was already accepted before
	}

	in := login("user@example.com", "correct-password")
	in.TOTPCode = "123456"
	_, err := e.service().Login(ctx, in)
	assertCode(t, err, apperr.CodeUnauthorized)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login_failed", e.audit.Entries[0].Action)

	// Compare against the plain wrong-code path (guard not involved) to
	// confirm both look the same to the caller.
	e2 := newFixture()
	withUser(e2, u)
	in2 := login("user@example.com", "correct-password")
	in2.TOTPCode = "000000"
	_, wrongCodeErr := e2.service().Login(ctx, in2)
	assertCode(t, wrongCodeErr, apperr.CodeUnauthorized)
	require.EqualError(t, err, wrongCodeErr.Error(), "replayed and wrong codes must be indistinguishable to the caller")
}

func TestLoginAdminAudited(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.Role = domain.RoleAdmin
	withUser(e, u)

	resp, err := e.service().Login(ctx, login("user@example.com", "correct-password"))
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.User.ClientID, "admins have no client profile")
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.login", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestLoginValidation(t *testing.T) {
	e := newFixture()
	_, err := e.service().Login(ctx, auth.LoginRequest{Email: "bad", Password: ""})
	assertCode(t, err, apperr.CodeValidation)
}

// Refresh / Logout

func TestRefreshRotates(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	withClient(e, &domain.Client{ID: 77, UserID: u.ID})
	e.tokens.consumeID = u.ID
	e.tokens.consumeJTI = "old-jti"

	resp, err := e.service().Refresh(ctx, "old-refresh")
	require.NoError(t, err)
	assert.Equal(t, []string{"old-refresh"}, e.tokens.consumed)
	require.Len(t, e.tokens.created, 1)
	assert.Equal(t, resp.RefreshToken, e.tokens.created[0])
	assert.NotEqual(t, "old-refresh", resp.RefreshToken)
	assert.Equal(t, int64(77), resp.User.ClientID)
}

func TestRefreshInvalidToken(t *testing.T) {
	e := newFixture()
	e.tokens.consumeErr = apperr.Unauthorized("refresh token invalid or expired")
	_, err := e.service().Refresh(ctx, "bogus")
	assertCode(t, err, apperr.CodeUnauthorized)
}

func TestRefreshInactiveUser(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.Status = domain.UserInactive
	withUser(e, u)
	e.tokens.consumeID = u.ID
	_, err := e.service().Refresh(ctx, "some-token")
	assertCode(t, err, apperr.CodeForbidden)
}

func TestRefreshEmptyToken(t *testing.T) {
	e := newFixture()
	_, err := e.service().Refresh(ctx, "")
	assertCode(t, err, apperr.CodeValidation)
}

func TestLogout(t *testing.T) {
	e := newFixture()
	var untouchedID int64
	e.presence.UntouchFn = func(_ context.Context, userID int64) error {
		untouchedID = userID
		return nil
	}

	require.NoError(t, e.service().Logout(ctx, 42, "some-refresh"))
	assert.Equal(t, []string{"some-refresh"}, e.tokens.revoked)
	assert.Equal(t, int64(42), untouchedID, "presence is untouched even without a refresh token check")

	require.NoError(t, e.service().Logout(ctx, 42, ""), "empty token is a no-op")
	assert.Len(t, e.tokens.revoked, 1)

	require.Len(t, e.audit.Entries, 2, "every logout call is audited, even the empty-token no-op")
	assert.Equal(t, "auth.logout", e.audit.Entries[0].Action)
	assert.Equal(t, int64(42), e.audit.Entries[0].ActorUserID)
}

func TestLogoutTogglesPresenceEvenWhenPresenceFails(t *testing.T) {
	e := newFixture()
	e.presence.UntouchFn = func(context.Context, int64) error {
		return errors.New("redis down")
	}
	require.NoError(t, e.service().Logout(ctx, 42, "some-refresh"), "a presence hiccup must never fail logout")
	assert.Equal(t, []string{"some-refresh"}, e.tokens.revoked)
	require.Len(t, e.audit.Entries, 1)
}

// Email verification / password reset

func TestVerifyEmail(t *testing.T) {
	e := newFixture()
	e.onetime.ConsumeFn = func(_ context.Context, kind, token string) (int64, error) {
		if kind == ports.TokenKindVerifyEmail && token == "good" {
			return 10, nil
		}
		return 0, apperr.NotFound("token")
	}
	var verifiedID int64
	var verifiedAt time.Time
	e.users.SetEmailVerifiedFn = func(_ context.Context, id int64, at time.Time) error {
		verifiedID, verifiedAt = id, at
		return nil
	}

	require.NoError(t, e.service().VerifyEmail(ctx, "good"))
	assert.Equal(t, int64(10), verifiedID)
	assert.Equal(t, e.now, verifiedAt)

	err := e.service().VerifyEmail(ctx, "bad")
	assertCode(t, err, apperr.CodeValidation)

	require.Len(t, e.audit.Entries, 1, "only the successful verification is audited")
	assert.Equal(t, "auth.verify_email", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestResendVerification(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)

	require.NoError(t, e.service().ResendVerification(ctx, u.Email))
	require.Len(t, e.sent, 1)
	assert.Equal(t, "verify_email", e.sent[0].Key)

	// Already verified: silently no-ops (public, anti-enumeration endpoint -
	// must not reveal verification status any more than account existence).
	verified := e.now
	u.EmailVerifiedAt = &verified
	require.NoError(t, e.service().ResendVerification(ctx, u.Email))
	require.Len(t, e.sent, 1, "no second email sent for an already-verified account")

	require.Len(t, e.audit.Entries, 1, "only the actual resend is audited")
	assert.Equal(t, "auth.resend_verification", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestResendVerificationUnknownEmailStillOK(t *testing.T) {
	e := newFixture()
	e.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
		return nil, apperr.NotFound("user")
	}
	require.NoError(t, e.service().ResendVerification(ctx, "nobody@example.com"))
	assert.Empty(t, e.sent)
}

func TestForgotPasswordUnknownEmailStillOK(t *testing.T) {
	e := newFixture()
	e.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
		return nil, apperr.NotFound("user")
	}
	var tokenCreated bool
	e.onetime.CreateFn = func(context.Context, string, int64, time.Duration) (string, error) {
		tokenCreated = true
		return "t", nil
	}
	require.NoError(t, e.service().ForgotPassword(ctx, "nobody@example.com"))
	assert.False(t, tokenCreated)
	assert.Empty(t, e.sent)
}

func TestForgotPasswordSendsResetLink(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	var gotKind string
	var gotTTL time.Duration
	e.onetime.CreateFn = func(_ context.Context, kind string, _ int64, ttl time.Duration) (string, error) {
		gotKind, gotTTL = kind, ttl
		return "reset-tok", nil
	}
	require.NoError(t, e.service().ForgotPassword(ctx, "User@Example.com"))
	assert.Equal(t, ports.TokenKindResetPassword, gotKind)
	assert.Equal(t, time.Hour, gotTTL)
	require.Len(t, e.sent, 1)
	assert.Equal(t, "reset_password", e.sent[0].Key)
	assert.Equal(t, "http://fe/reset-password?token=reset-tok", e.sent[0].Data["ResetURL"])

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.forgot_password", e.audit.Entries[0].Action)
	assert.Equal(t, int64(10), e.audit.Entries[0].ActorUserID)
}

func TestResetPassword(t *testing.T) {
	e := newFixture()
	e.onetime.ConsumeFn = func(_ context.Context, kind, token string) (int64, error) {
		if kind == ports.TokenKindResetPassword && token == "good" {
			return 10, nil
		}
		return 0, apperr.NotFound("token")
	}
	var gotID int64
	var gotHash string
	e.users.UpdatePasswordFn = func(_ context.Context, id int64, hash string) error {
		gotID, gotHash = id, hash
		return nil
	}

	require.NoError(t, e.service().ResetPassword(ctx, auth.ResetPasswordRequest{
		Token: "good", Password: "newpassword1",
	}))
	assert.Equal(t, int64(10), gotID)
	assert.Equal(t, "hashed:newpassword1", gotHash)
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.password_reset", e.audit.Entries[0].Action)
	assert.Equal(t, []int64{10}, e.tokens.revokedAll, "reset must revoke every session")

	err := e.service().ResetPassword(ctx, auth.ResetPasswordRequest{Token: "bad", Password: "newpassword1"})
	assertCode(t, err, apperr.CodeValidation)

	err = e.service().ResetPassword(ctx, auth.ResetPasswordRequest{Token: "good", Password: "short"})
	assertCode(t, err, apperr.CodeValidation)
}

// Me / UpdateMe

func TestMeClient(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	withClient(e, &domain.Client{ID: 77, UserID: u.ID, FirstName: "Budi"})

	me, err := e.service().Me(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(77), me.User.ClientID)
	require.NotNil(t, me.Client)
	assert.Equal(t, "Budi", me.Client.FirstName)
}

func TestMeAdminHasNoClient(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.Role = domain.RoleAdmin
	withUser(e, u)

	me, err := e.service().Me(ctx, u.ID)
	require.NoError(t, err)
	assert.Nil(t, me.Client)
	assert.Equal(t, int64(0), me.User.ClientID)
}

func TestMeTouchesPresenceForAdminAndStaff(t *testing.T) {
	for _, role := range []domain.UserRole{domain.RoleAdmin, domain.RoleStaff} {
		e := newFixture()
		u := activeUser()
		u.Role = role
		withUser(e, u)

		var gotID int64
		var gotEmail, gotRole string
		e.presence.TouchFn = func(_ context.Context, userID int64, email, r string) error {
			gotID, gotEmail, gotRole = userID, email, r
			return nil
		}

		_, err := e.service().Me(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, u.ID, gotID)
		assert.Equal(t, u.Email, gotEmail)
		assert.Equal(t, string(role), gotRole)
	}
}

func TestMeDoesNotTouchPresenceForClient(t *testing.T) {
	e := newFixture()
	u := activeUser() // role client
	withClient(e, &domain.Client{ID: 77, UserID: u.ID})
	withUser(e, u)

	touched := false
	e.presence.TouchFn = func(context.Context, int64, string, string) error {
		touched = true
		return nil
	}

	_, err := e.service().Me(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, touched, "client sessions are not tracked as staff presence")
}

func TestMeToleratesPresenceFailure(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.Role = domain.RoleAdmin
	withUser(e, u)
	e.presence.TouchFn = func(context.Context, int64, string, string) error {
		return errors.New("redis down")
	}

	_, err := e.service().Me(ctx, u.ID)
	require.NoError(t, err, "a presence hiccup must never fail /auth/me")
}

func TestMeUnknownUser(t *testing.T) {
	e := newFixture()
	e.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
		return nil, apperr.NotFound("user")
	}
	_, err := e.service().Me(ctx, 99)
	assertCode(t, err, apperr.CodeNotFound)
}

func strPtr(s string) *string { return &s }

func TestUpdateMeLocale(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	var updated *domain.User
	e.users.UpdateFn = func(_ context.Context, uu *domain.User) error { updated = uu; return nil }

	me, err := e.service().UpdateMe(ctx, u.ID, auth.UpdateMeRequest{Locale: strPtr("en")})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "en", updated.Locale)
	assert.Equal(t, "en", me.User.Locale)
}

func TestUpdateMeProfileFields(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	withClient(e, &domain.Client{ID: 77, UserID: u.ID, FirstName: "Old", Phone: "111"})
	var updated *domain.Client
	e.clients.UpdateFn = func(_ context.Context, c *domain.Client) error { updated = c; return nil }

	me, err := e.service().UpdateMe(ctx, u.ID, auth.UpdateMeRequest{
		FirstName: strPtr("Baru"),
		Phone:     strPtr("0899"),
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "Baru", updated.FirstName)
	assert.Equal(t, "0899", updated.Phone)
	assert.Equal(t, "Baru", me.Client.FirstName)
}

func TestUpdateMePasswordChange(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	var gotHash string
	e.users.UpdatePasswordFn = func(_ context.Context, _ int64, hash string) error {
		gotHash = hash
		return nil
	}

	_, err := e.service().UpdateMe(ctx, u.ID, auth.UpdateMeRequest{
		CurrentPassword: "correct-password",
		NewPassword:     "brand-new-pass1",
	})
	require.NoError(t, err)
	assert.Equal(t, "hashed:brand-new-pass1", gotHash)
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.password_change", e.audit.Entries[0].Action)
	assert.Equal(t, []int64{u.ID}, e.tokens.revokedAll, "password change must revoke every session")
}

func TestResetPasswordRevokeAllFails(t *testing.T) {
	e := newFixture()
	e.onetime.ConsumeFn = func(_ context.Context, kind, token string) (int64, error) {
		return 10, nil
	}
	e.users.UpdatePasswordFn = func(_ context.Context, _ int64, _ string) error { return nil }
	e.tokens.revokeAllErr = errors.New("redis down")

	err := e.service().ResetPassword(ctx, auth.ResetPasswordRequest{Token: "good", Password: "newpassword1"})
	assertCode(t, err, apperr.CodeInternal)
}

func TestUpdateMePasswordWrongCurrent(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	_, err := e.service().UpdateMe(ctx, 10, auth.UpdateMeRequest{
		CurrentPassword: "wrong",
		NewPassword:     "brand-new-pass1",
	})
	assertCode(t, err, apperr.CodeUnauthorized)
}

func TestUpdateMePasswordMissingCurrent(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	_, err := e.service().UpdateMe(ctx, 10, auth.UpdateMeRequest{NewPassword: "brand-new-pass1"})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpdateMeValidation(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	_, err := e.service().UpdateMe(ctx, 10, auth.UpdateMeRequest{Locale: strPtr("xx")})
	assertCode(t, err, apperr.CodeValidation)
}

// 2FA

func TestTwoFASetup(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	e.enc.EncryptFn = func(plain string) (string, error) { return "enc(" + plain + ")", nil }
	var updated *domain.User
	e.users.UpdateFn = func(_ context.Context, uu *domain.User) error { updated = uu; return nil }

	resp, err := e.service().TwoFASetup(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "SECRET", resp.Secret)
	assert.Contains(t, resp.OTPAuthURL, "otpauth://totp/TestPanel:user@example.com")
	require.NotNil(t, updated)
	assert.Equal(t, "enc(SECRET)", updated.TwoFASecretEnc)
	assert.False(t, updated.TwoFAEnabled, "setup does not enable 2FA")
}

func TestTwoFASetupAlreadyEnabled(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	withUser(e, u)
	_, err := e.service().TwoFASetup(ctx, u.ID)
	assertCode(t, err, apperr.CodeConflict)
}

func TestTwoFAEnable(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)
	var updated *domain.User
	e.users.UpdateFn = func(_ context.Context, uu *domain.User) error { updated = uu; return nil }

	require.NoError(t, e.service().TwoFAEnable(ctx, u.ID, auth.TwoFAEnableRequest{Code: "123456"}))
	require.NotNil(t, updated)
	assert.True(t, updated.TwoFAEnabled)
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.2fa_enable", e.audit.Entries[0].Action)
}

func TestTwoFAEnableWrongCode(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)
	err := e.service().TwoFAEnable(ctx, u.ID, auth.TwoFAEnableRequest{Code: "654321"})
	assertCode(t, err, apperr.CodeUnauthorized)
}

func TestTwoFAEnableWithoutSetup(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	err := e.service().TwoFAEnable(ctx, 10, auth.TwoFAEnableRequest{Code: "123456"})
	assertCode(t, err, apperr.CodeConflict)
}

func TestTwoFAEnableAlreadyEnabled(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)
	err := e.service().TwoFAEnable(ctx, u.ID, auth.TwoFAEnableRequest{Code: "123456"})
	assertCode(t, err, apperr.CodeConflict)
}

func TestTwoFAEnableValidation(t *testing.T) {
	e := newFixture()
	err := e.service().TwoFAEnable(ctx, 10, auth.TwoFAEnableRequest{Code: "12"})
	assertCode(t, err, apperr.CodeValidation)
}

func TestTwoFADisable(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)
	var updated *domain.User
	e.users.UpdateFn = func(_ context.Context, uu *domain.User) error { updated = uu; return nil }

	require.NoError(t, e.service().TwoFADisable(ctx, u.ID, auth.TwoFADisableRequest{
		Password: "correct-password", Code: "123456",
	}))
	require.NotNil(t, updated)
	assert.False(t, updated.TwoFAEnabled)
	assert.Empty(t, updated.TwoFASecretEnc, "secret cleared on disable")
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.2fa_disable", e.audit.Entries[0].Action)
}

func TestTwoFADisableWrongPassword(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)
	err := e.service().TwoFADisable(ctx, u.ID, auth.TwoFADisableRequest{Password: "wrong", Code: "123456"})
	assertCode(t, err, apperr.CodeUnauthorized)
}

func TestTwoFADisableWrongCode(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.TwoFAEnabled = true
	u.TwoFASecretEnc = "SECRET"
	withUser(e, u)
	err := e.service().TwoFADisable(ctx, u.ID, auth.TwoFADisableRequest{Password: "correct-password", Code: "000000"})
	assertCode(t, err, apperr.CodeUnauthorized)
}

func TestTwoFADisableNotEnabled(t *testing.T) {
	e := newFixture()
	withUser(e, activeUser())
	err := e.service().TwoFADisable(ctx, 10, auth.TwoFADisableRequest{Password: "correct-password", Code: "123456"})
	assertCode(t, err, apperr.CodeConflict)
}

// Impersonation

func TestImpersonate(t *testing.T) {
	e := newFixture()
	u := activeUser()
	withUser(e, u)
	withClient(e, &domain.Client{ID: 55, UserID: u.ID})

	resp, err := e.service().Impersonate(ctx, 1, 55, "9.9.9.9")
	require.NoError(t, err)
	require.Len(t, e.tokens.issued, 1)
	assert.Equal(t, issuedAccess{10, "client", 55}, e.tokens.issued[0])
	assert.Equal(t, int64(55), resp.User.ClientID)

	require.Len(t, e.audit.Entries, 1)
	entry := e.audit.Entries[0]
	assert.Equal(t, "auth.impersonate", entry.Action)
	assert.Equal(t, int64(1), entry.ActorUserID, "audited as the admin actor")
	assert.Equal(t, "client", entry.Entity)
	assert.Equal(t, int64(55), entry.EntityID)
}

func TestImpersonateUnknownClient(t *testing.T) {
	e := newFixture()
	e.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) {
		return nil, apperr.NotFound("client")
	}
	_, err := e.service().Impersonate(ctx, 1, 999, "")
	assertCode(t, err, apperr.CodeNotFound)
	assert.Empty(t, e.audit.Entries)
}

func TestImpersonateInactiveUser(t *testing.T) {
	e := newFixture()
	u := activeUser()
	u.Status = domain.UserInactive
	withUser(e, u)
	withClient(e, &domain.Client{ID: 55, UserID: u.ID})
	_, err := e.service().Impersonate(ctx, 1, 55, "")
	assertCode(t, err, apperr.CodeConflict)
	assert.Empty(t, e.tokens.issued)
}
