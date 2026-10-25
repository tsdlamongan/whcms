package ports_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistrarCallError_Error(t *testing.T) {
	cause := errors.New("connection reset")
	tests := []struct {
		name string
		err  *ports.RegistrarCallError
		want string
	}{
		{"not sent", &ports.RegistrarCallError{NotSent: true, Message: "circuit breaker open"}, "not sent: circuit breaker open"},
		{"no response", &ports.RegistrarCallError{Message: "timeout"}, "no response: timeout"},
		{"http status", &ports.RegistrarCallError{HTTPStatus: 409}, "http 409"},
		{"retry after", &ports.RegistrarCallError{HTTPStatus: 429, RetryAfter: 2 * time.Second}, "http 429 (retry after 2s)"},
		{"with message", &ports.RegistrarCallError{HTTPStatus: 409, Message: "Domain not available"}, "http 409: Domain not available"},
		{"retry after and message", &ports.RegistrarCallError{HTTPStatus: 429, RetryAfter: time.Second, Message: "slow down"}, "http 429 (retry after 1s): slow down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.err.Error())
		})
	}

	wrapped := &ports.RegistrarCallError{Message: "x", Err: cause}
	assert.ErrorIs(t, wrapped, cause)
}

func TestRegistrarCallErrorOf(t *testing.T) {
	assert.Nil(t, ports.RegistrarCallErrorOf(nil))
	assert.Nil(t, ports.RegistrarCallErrorOf(errors.New("plain")))
	assert.Nil(t, ports.RegistrarCallErrorOf(apperr.Validation("bad")))

	ce := &ports.RegistrarCallError{HTTPStatus: 429, RetryAfter: time.Second}
	viaApperr := apperr.New(apperr.CodeRateLimited, "rdash: slow down").WithCause(ce)
	got := ports.RegistrarCallErrorOf(viaApperr)
	require.NotNil(t, got)
	assert.Equal(t, 429, got.HTTPStatus)

	// Found through a further fmt.Errorf wrap too.
	got = ports.RegistrarCallErrorOf(fmt.Errorf("hunt: %w", viaApperr))
	require.NotNil(t, got)
	assert.Equal(t, time.Second, got.RetryAfter)
}
