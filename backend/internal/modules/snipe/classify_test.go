package snipe

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

func TestClassify(t *testing.T) {
	cfg := defaultConfig()
	tests := []struct {
		name     string
		err      error
		kind     outcomeKind
		wait     time.Duration
		msgMatch string
	}{
		{"taken 409", errTaken(), outTaken, 0, "Domain not available"},
		{"conflict code without wire detail", apperr.Conflict("exists"), outTaken, 0, "exists"},
		{"429 uses the configured backoff", errRateLimited(0), outBackoff, cfg.RateLimitBackoff, "Too Many Requests"},
		{"429 honours a longer Retry-After", errRateLimited(7 * time.Second), outBackoff, 7 * time.Second, ""},
		{"429 ignores a shorter Retry-After", errRateLimited(time.Second), outBackoff, cfg.RateLimitBackoff, ""},
		{"rate limited code without wire detail", apperr.New(apperr.CodeRateLimited, "slow"), outBackoff, cfg.RateLimitBackoff, "slow"},
		{"401 pauses", errUnauthorized(), outPause, 0, "credentials"},
		{
			"403 pauses",
			apperr.New(apperr.CodeExternal, "rdash: Forbidden").WithCause(&ports.RegistrarCallError{HTTPStatus: 403, Message: "Forbidden"}),
			outPause, 0, "credentials",
		},
		{"timeout is ambiguous", errAmbiguous(), outAmbiguous, 0, "timeout"},
		{
			"5xx is ambiguous",
			apperr.New(apperr.CodeExternal, "rdash: down").WithCause(&ports.RegistrarCallError{HTTPStatus: 503, Message: "down", Ambiguous: true}),
			outAmbiguous, 0, "down",
		},
		{"breaker open was never sent", errNotSent(), outNotSent, cfg.RateLimitBackoff, "circuit breaker"},
		{"insufficient balance pauses", errRejected("Insufficient balance"), outPause, 0, "Insufficient balance"},
		{"saldo (indonesian) pauses", errRejected("Saldo tidak cukup"), outPause, 0, "Saldo"},
		{"deposit wording pauses", errRejected("Please deposit funds"), outPause, 0, "deposit"},
		{
			"customer problem fails the snipe",
			errRejected("The selected customer id is invalid.", apperr.FieldError{Field: "customer_id", Message: "invalid"}),
			outFail, 0, "customer",
		},
		{"other validation is counted and retried", errRejected("The name is reserved."), outRetry, 0, "reserved"},
		{
			"4xx without a mapped code is retried",
			apperr.New(apperr.CodeExternal, "rdash: gone").WithCause(&ports.RegistrarCallError{HTTPStatus: 404, Message: "gone"}),
			outRetry, 0, "gone",
		},
		{
			"4xx account wording pauses",
			apperr.New(apperr.CodeExternal, "rdash: no funds").WithCause(&ports.RegistrarCallError{HTTPStatus: 402, Message: "Not enough funds"}),
			outPause, 0, "Not enough",
		},
		{"an error with no wire detail is ambiguous", errors.New("who knows"), outAmbiguous, 0, "who knows"},
		{
			"2xx external without ambiguity flag is still verified",
			apperr.New(apperr.CodeExternal, "rdash: odd").WithCause(&ports.RegistrarCallError{HTTPStatus: 200, Message: "odd"}),
			outAmbiguous, 0, "odd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.err, cfg, "")
			assert.Equal(t, tt.kind, got.kind, "kind (msg %q)", got.msg)
			assert.Equal(t, tt.wait, got.wait)
			if tt.msgMatch != "" {
				assert.Contains(t, got.msg, tt.msgMatch)
			}
		})
	}
}

func TestClassifyTakenWordingAndNameScrubbing(t *testing.T) {
	cfg := defaultConfig()
	// A 4xx that says the name is not available is just "still taken".
	assert.Equal(t, outTaken, classify(errRejected("The domain is not available"), cfg, "").kind)
	assert.Equal(t, outTaken, classify(errRejected("Already registered by someone"), cfg, "").kind)

	// The registrar echoes the name: it must not be able to spell out a pause.
	assert.Equal(t, outPause, classify(errRejected("mybalance.com is not available"), cfg, "").kind,
		"without the name known, 'balance' looks like an account problem")
	got := classify(errRejected("mybalance.com is not available"), cfg, "MyBalance.com")
	assert.Equal(t, outTaken, got.kind, "with the name scrubbed it is a plain refusal")
	assert.Contains(t, got.msg, "mybalance.com", "the original text is still reported")

	// A real balance complaint survives the scrubbing.
	assert.Equal(t, outPause, classify(errRejected("Insufficient balance for example.com"), cfg, "example.com").kind)
}

func TestClassifyCapsRetryAfter(t *testing.T) {
	cfg := defaultConfig()
	got := classify(errRateLimited(time.Hour), cfg, "")
	assert.Equal(t, outBackoff, got.kind)
	assert.Equal(t, maxBackoff, got.wait, "a registrar asking for an hour must not park a hunt through its window")
}

func TestClassifyFlagsBalancePausesOnly(t *testing.T) {
	cfg := defaultConfig()
	assert.True(t, classify(errRejected("Insufficient balance"), cfg, "").balance, "money problems wait for a top-up")
	assert.True(t, classify(apperr.New(apperr.CodeExternal, "x").WithCause(
		&ports.RegistrarCallError{HTTPStatus: 402, Message: "Not enough funds"}), cfg, "").balance)
	assert.False(t, classify(errUnauthorized(), cfg, "").balance, "rejected credentials just need to work again")
}

func TestErrTextFallbacks(t *testing.T) {
	plain := errors.New("plain failure")
	assert.Equal(t, "plain failure", errText(plain, nil, nil))
	assert.Equal(t, "app msg", errText(plain, apperr.New(apperr.CodeExternal, "app msg"), nil))
	assert.Equal(t, "wire msg", errText(plain, apperr.New(apperr.CodeExternal, "app msg"), &ports.RegistrarCallError{Message: "wire msg"}))
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, isNotFound(apperr.NotFound("domain")))
	assert.True(t, isNotFound(fmt.Errorf("wrapped: %w", apperr.NotFound("domain"))))
	assert.False(t, isNotFound(apperr.Conflict("x")))
	assert.False(t, isNotFound(errors.New("x")))
	assert.False(t, isNotFound(nil))
}

func TestIsCtxErr(t *testing.T) {
	live := context.Background()
	assert.False(t, isCtxErr(live, context.Canceled), "a live context means the error is not ours to swallow")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.True(t, isCtxErr(cancelled, context.Canceled))
	assert.True(t, isCtxErr(cancelled, fmt.Errorf("call: %w", context.Canceled)))
	assert.False(t, isCtxErr(cancelled, errors.New("unrelated")))

	timedOut, cancelT := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelT()
	<-timedOut.Done()
	assert.True(t, isCtxErr(timedOut, context.DeadlineExceeded))
}
