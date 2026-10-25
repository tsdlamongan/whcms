package mocks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The domain document / verification seams: MockRegistrarModule's upload-link
// and resend methods, MockDomainRepo.ListAwaitingVerification and
// MockDomainPaymentSource.

func TestMockRegistrarModule_DocumentSeams(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	zero := &mocks.MockRegistrarModule{}
	link, err := zero.DocumentUploadLink(ctx, "a.co.id")
	require.NoError(t, err)
	assert.Empty(t, link)
	assert.NoError(t, zero.ResendVerification(ctx, "a.my.id"))

	m := &mocks.MockRegistrarModule{
		DocumentUploadLinkFn: func(ctx context.Context, name string) (string, error) { return "https://x/" + name, boom },
		ResendVerificationFn: func(ctx context.Context, name string) error { return boom },
	}
	link, err = m.DocumentUploadLink(ctx, "a.co.id")
	assert.Equal(t, "https://x/a.co.id", link)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, m.ResendVerification(ctx, "a.my.id"), boom)
}

func TestMockDomainRepo_ListAwaitingVerification(t *testing.T) {
	ctx := context.Background()

	list, err := (&mocks.MockDomainRepo{}).ListAwaitingVerification(ctx, 10)
	require.NoError(t, err)
	assert.Nil(t, list)

	m := &mocks.MockDomainRepo{ListAwaitingVerificationFn: func(ctx context.Context, limit int) ([]domain.Domain, error) {
		return []domain.Domain{{ID: int64(limit)}}, nil
	}}
	list, err = m.ListAwaitingVerification(ctx, 7)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.EqualValues(t, 7, list[0].ID)
}

func TestMockDomainPaymentSource(t *testing.T) {
	ctx := context.Background()

	pay, err := (&mocks.MockDomainPaymentSource{}).RegistrationPayment(ctx, 1)
	require.NoError(t, err)
	assert.Nil(t, pay)

	m := &mocks.MockDomainPaymentSource{RegistrationPaymentFn: func(ctx context.Context, id int64) (*ports.DomainRegistrationPayment, error) {
		return &ports.DomainRegistrationPayment{InvoiceID: id, Amount: 5}, nil
	}}
	pay, err = m.RegistrationPayment(ctx, 9)
	require.NoError(t, err)
	assert.EqualValues(t, 9, pay.InvoiceID)
}
