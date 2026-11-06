package domain_test

import (
	"errors"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnipeTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.SnipeStatus
		ok       bool
	}{
		{domain.SnipePendingPayment, domain.SnipeActive, true},
		{domain.SnipePendingPayment, domain.SnipeCancelled, true},
		{domain.SnipePendingPayment, domain.SnipeFailed, true},
		{domain.SnipePendingPayment, domain.SnipeWon, false},
		{domain.SnipePendingPayment, domain.SnipeExpired, false},
		{domain.SnipeActive, domain.SnipeWon, true},
		{domain.SnipeActive, domain.SnipeExpired, true},
		{domain.SnipeActive, domain.SnipeCancelled, true},
		{domain.SnipeActive, domain.SnipeFailed, true},
		{domain.SnipeActive, domain.SnipePendingPayment, false},
		{domain.SnipeWon, domain.SnipeActive, false},
		{domain.SnipeExpired, domain.SnipeActive, false},
		{domain.SnipeCancelled, domain.SnipeActive, false},
		{domain.SnipeFailed, domain.SnipeActive, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.ok, domain.SnipeCanTransition(tt.from, tt.to), "%s -> %s", tt.from, tt.to)
	}
}

func TestSnipeTerminal(t *testing.T) {
	for _, s := range []domain.SnipeStatus{domain.SnipeWon, domain.SnipeExpired, domain.SnipeCancelled, domain.SnipeFailed} {
		assert.True(t, domain.SnipeTerminal(s), s)
	}
	for _, s := range []domain.SnipeStatus{domain.SnipePendingPayment, domain.SnipeActive, ""} {
		assert.False(t, domain.SnipeTerminal(s), s)
	}
}

func TestTransitionSnipe(t *testing.T) {
	got, err := domain.TransitionSnipe(domain.SnipeActive, domain.SnipeWon)
	require.NoError(t, err)
	assert.Equal(t, domain.SnipeWon, got)

	got, err = domain.TransitionSnipe(domain.SnipeWon, domain.SnipeActive)
	assert.Equal(t, domain.SnipeWon, got)
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeConflict, ae.Code)
}
