package domain_test

import (
	"errors"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvoiceTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.InvoiceStatus
		ok       bool
	}{
		{domain.InvoiceDraft, domain.InvoiceUnpaid, true},
		{domain.InvoiceDraft, domain.InvoicePaid, false},
		{domain.InvoiceDraft, domain.InvoiceCancelled, false},
		{domain.InvoiceUnpaid, domain.InvoicePaid, true},
		{domain.InvoiceUnpaid, domain.InvoiceCancelled, true},
		{domain.InvoiceUnpaid, domain.InvoiceOverdue, true},
		{domain.InvoiceUnpaid, domain.InvoiceRefunded, false},
		{domain.InvoiceOverdue, domain.InvoicePaid, true},
		{domain.InvoiceOverdue, domain.InvoiceCancelled, true},
		{domain.InvoiceOverdue, domain.InvoiceUnpaid, false},
		{domain.InvoicePaid, domain.InvoiceRefunded, true},
		{domain.InvoicePaid, domain.InvoiceUnpaid, false},
		{domain.InvoicePaid, domain.InvoicePaid, false},
		{domain.InvoiceRefunded, domain.InvoicePaid, false},
		{domain.InvoiceCancelled, domain.InvoiceUnpaid, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.ok, domain.InvoiceCanTransition(tt.from, tt.to),
			"%s -> %s", tt.from, tt.to)
	}
}

func TestOrderTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.OrderStatus
		ok       bool
	}{
		{domain.OrderPending, domain.OrderActive, true},
		{domain.OrderPending, domain.OrderFraud, true},
		{domain.OrderPending, domain.OrderCancelled, true},
		{domain.OrderActive, domain.OrderCancelled, false},
		{domain.OrderActive, domain.OrderPending, false},
		{domain.OrderFraud, domain.OrderActive, false},
		{domain.OrderCancelled, domain.OrderActive, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.ok, domain.OrderCanTransition(tt.from, tt.to),
			"%s -> %s", tt.from, tt.to)
	}
}

func TestServiceTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.ServiceStatus
		ok       bool
	}{
		{domain.ServicePending, domain.ServiceActive, true},
		{domain.ServicePending, domain.ServiceCancelled, true},
		{domain.ServicePending, domain.ServiceSuspended, false},
		{domain.ServiceActive, domain.ServiceSuspended, true},
		{domain.ServiceActive, domain.ServiceTerminated, true},
		{domain.ServiceActive, domain.ServiceCancelled, false},
		{domain.ServiceSuspended, domain.ServiceActive, true},
		{domain.ServiceSuspended, domain.ServiceTerminated, true},
		{domain.ServiceSuspended, domain.ServiceCancelled, false},
		{domain.ServiceTerminated, domain.ServiceActive, false},
		{domain.ServiceCancelled, domain.ServiceActive, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.ok, domain.ServiceCanTransition(tt.from, tt.to),
			"%s -> %s", tt.from, tt.to)
	}
}

func TestDomainTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.DomainStatus
		ok       bool
	}{
		{domain.DomainPending, domain.DomainActive, true},
		{domain.DomainPending, domain.DomainCancelled, true},
		{domain.DomainPending, domain.DomainExpired, false},
		{domain.DomainActive, domain.DomainExpired, true},
		{domain.DomainActive, domain.DomainCancelled, false},
		{domain.DomainPendingTransfer, domain.DomainActive, true},
		{domain.DomainPendingTransfer, domain.DomainCancelled, true},
		{domain.DomainExpired, domain.DomainActive, true},
		{domain.DomainExpired, domain.DomainCancelled, false},
		{domain.DomainCancelled, domain.DomainActive, false},
		// registrar document/verification lifecycle
		{domain.DomainPending, domain.DomainRejected, true},
		{domain.DomainActive, domain.DomainSuspended, true},
		{domain.DomainActive, domain.DomainRejected, true},
		{domain.DomainPendingTransfer, domain.DomainRejected, true},
		{domain.DomainPendingTransfer, domain.DomainSuspended, false},
		{domain.DomainSuspended, domain.DomainActive, true},
		{domain.DomainSuspended, domain.DomainRejected, true},
		{domain.DomainSuspended, domain.DomainCancelled, true},
		{domain.DomainSuspended, domain.DomainExpired, false},
		{domain.DomainRejected, domain.DomainActive, false},
		{domain.DomainRejected, domain.DomainCancelled, false},
		{domain.DomainExpired, domain.DomainSuspended, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.ok, domain.DomainCanTransition(tt.from, tt.to),
			"%s -> %s", tt.from, tt.to)
	}
}

func TestTypedTransitionFuncs(t *testing.T) {
	t.Run("invoice ok", func(t *testing.T) {
		got, err := domain.TransitionInvoice(domain.InvoiceUnpaid, domain.InvoicePaid)
		require.NoError(t, err)
		assert.Equal(t, domain.InvoicePaid, got)
	})
	t.Run("invoice illegal returns conflict", func(t *testing.T) {
		got, err := domain.TransitionInvoice(domain.InvoicePaid, domain.InvoiceUnpaid)
		assert.Equal(t, domain.InvoicePaid, got)
		var ae *apperr.Error
		require.True(t, errors.As(err, &ae))
		assert.Equal(t, apperr.CodeConflict, ae.Code)
	})
	t.Run("order", func(t *testing.T) {
		got, err := domain.TransitionOrder(domain.OrderPending, domain.OrderActive)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderActive, got)
		_, err = domain.TransitionOrder(domain.OrderActive, domain.OrderPending)
		assert.Error(t, err)
	})
	t.Run("service", func(t *testing.T) {
		got, err := domain.TransitionService(domain.ServiceSuspended, domain.ServiceActive)
		require.NoError(t, err)
		assert.Equal(t, domain.ServiceActive, got)
		_, err = domain.TransitionService(domain.ServiceTerminated, domain.ServiceActive)
		assert.Error(t, err)
	})
	t.Run("domain", func(t *testing.T) {
		got, err := domain.TransitionDomain(domain.DomainExpired, domain.DomainActive)
		require.NoError(t, err)
		assert.Equal(t, domain.DomainActive, got)
		_, err = domain.TransitionDomain(domain.DomainCancelled, domain.DomainActive)
		assert.Error(t, err)
	})
}
