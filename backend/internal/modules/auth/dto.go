package auth

import (
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
)

// RegisterRequest is the POST /auth/register body. Locale is optional and
// defaults to "id" (users.locale column default). CaptchaToken is verified only
// when CAPTCHA is enabled (security.captcha_enabled). IP is filled by the
// handler (never from the body). Address1/City/State/Postcode are required at
// registration (not just profile-edit time): a registrar needs a complete
// registrant contact to register or transfer a domain, and rejecting an
// incomplete address here is cheaper than failing that job asynchronously,
// post-payment (domains.Service.registrantContact).
type RegisterRequest struct {
	Email        string `json:"email" validate:"required,email,max=255"`
	Password     string `json:"password" validate:"required,min=8,max=72"`
	FirstName    string `json:"first_name" validate:"required,max=100"`
	LastName     string `json:"last_name" validate:"required,max=100"`
	Company      string `json:"company" validate:"max=150"`
	Address1     string `json:"address1" validate:"required,max=200"`
	Address2     string `json:"address2" validate:"max=200"`
	City         string `json:"city" validate:"required,max=100"`
	State        string `json:"state" validate:"required,max=100"`
	Postcode     string `json:"postcode" validate:"required,max=20"`
	Country      string `json:"country" validate:"omitempty,len=2"`
	Phone        string `json:"phone" validate:"max=30"`
	Locale       string `json:"locale" validate:"omitempty,oneof=id en"`
	CaptchaToken string `json:"captcha_token"`
	IP           string `json:"-"`
}

// LoginRequest is the POST /auth/login body. TOTPCode is required when the
// user has 2FA enabled. CaptchaToken is verified only when CAPTCHA is enabled
// (security.captcha_enabled). IP is filled by the handler (never from the body).
type LoginRequest struct {
	Email        string `json:"email" validate:"required,email"`
	Password     string `json:"password" validate:"required"`
	TOTPCode     string `json:"totp_code"`
	CaptchaToken string `json:"captcha_token"`
	IP           string `json:"-"`
}

// RefreshRequest is the POST /auth/refresh body.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// LogoutRequest is the POST /auth/logout body. The refresh token to revoke;
// optional (logout without it only discards the client-side access token).
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// VerifyEmailRequest is the POST /auth/verify-email body.
type VerifyEmailRequest struct {
	Token string `json:"token" validate:"required"`
}

// ForgotPasswordRequest is the POST /auth/forgot-password body.
type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResendVerificationRequest is the POST /auth/resend-verification body.
type ResendVerificationRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest is the POST /auth/reset-password body.
type ResetPasswordRequest struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=8,max=72"`
}

// UpdateMeRequest is the PATCH /auth/me body. Email is immutable. Profile
// pointer fields update the client profile when present; NewPassword (with
// CurrentPassword) changes the password.
type UpdateMeRequest struct {
	FirstName       *string `json:"first_name" validate:"omitempty,max=100"`
	LastName        *string `json:"last_name" validate:"omitempty,max=100"`
	Company         *string `json:"company" validate:"omitempty,max=150"`
	Phone           *string `json:"phone" validate:"omitempty,max=30"`
	Locale          *string `json:"locale" validate:"omitempty,oneof=id en"`
	CurrentPassword string  `json:"current_password"`
	NewPassword     string  `json:"new_password" validate:"omitempty,min=8,max=72"`
}

// TwoFAEnableRequest is the POST /auth/2fa/enable body.
type TwoFAEnableRequest struct {
	Code string `json:"code" validate:"required,len=6,numeric"`
}

// TwoFADisableRequest is the POST /auth/2fa/disable body.
type TwoFADisableRequest struct {
	Password string `json:"password" validate:"required"`
	Code     string `json:"code" validate:"required,len=6,numeric"`
}

// UserDTO is the public representation of a user account.
type UserDTO struct {
	ID            int64      `json:"id"`
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	Status        string     `json:"status"`
	Locale        string     `json:"locale"`
	TwoFAEnabled  bool       `json:"twofa_enabled"`
	EmailVerified bool       `json:"email_verified"`
	ClientID      int64      `json:"client_id"` // 0 for staff/admin
	LastLoginAt   *time.Time `json:"last_login_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AuthResponse carries a token pair plus the user DTO (login/refresh/register/
// impersonate).
type AuthResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	User         UserDTO `json:"user"`
}

// MeResponse is the GET /auth/me payload: the user plus the client profile
// (nil for staff/admin).
type MeResponse struct {
	User   UserDTO        `json:"user"`
	Client *domain.Client `json:"client"`
}

// TwoFASetupResponse is the POST /auth/2fa/setup payload.
type TwoFASetupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

func userDTO(u *domain.User, clientID int64) UserDTO {
	return UserDTO{
		ID:            u.ID,
		Email:         u.Email,
		Role:          string(u.Role),
		Status:        string(u.Status),
		Locale:        u.Locale,
		TwoFAEnabled:  u.TwoFAEnabled,
		EmailVerified: u.EmailVerifiedAt != nil,
		ClientID:      clientID,
		LastLoginAt:   u.LastLoginAt,
		CreatedAt:     u.CreatedAt,
	}
}
