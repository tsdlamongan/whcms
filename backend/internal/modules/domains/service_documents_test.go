package domains_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Document / verification lifecycle: sync mirroring, rejection + refund
// policy, registration of document-requiring TLDs, the client upload link and
// the admin refund override. Rules under test come from the registrar's
// documentation (docs.dewabiz.co.id "Auto Activation").

type mail struct {
	userID int64
	key    string
	data   map[string]any
}

// recordMail captures every SendTemplate call.
func recordMail(d *deps) *[]mail {
	var got []mail
	d.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
		got = append(got, mail{userID, key, data})
		return nil
	}
	return &got
}

// recordAlerts captures every AlertAdmin call as "subject | message".
func recordAlerts(d *deps) *[]string {
	var got []string
	d.notifier.AlertAdminFn = func(ctx context.Context, subject, message string) error {
		got = append(got, subject+" | "+message)
		return nil
	}
	return &got
}

// captureUpdates records the domain row on every Domains.Update.
func captureUpdates(d *deps) *[]*domain.Domain {
	var got []*domain.Domain
	d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
		cp := *dm
		got = append(got, &cp)
		return nil
	}
	return &got
}

func syncReturns(d *deps, info ports.DomainSyncInfo) {
	d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
		cp := info
		return &cp, nil
	}
}

func day(y int, m time.Month, dd int) *time.Time {
	t := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
	return &t
}

// docDomain is a document-requiring .co.id domain in the first days of its
// auto-activation window.
func docDomain(over func(*domain.Domain)) *domain.Domain {
	return testDomain(func(x *domain.Domain) {
		x.Name = "toko.co.id"
		x.RegistrationDate = day(2026, 7, 1)
		x.DocumentRequired = true
		x.Verification = domain.VerificationWaiting
		if over != nil {
			over(x)
		}
	})
}

func verification(required bool, v domain.DomainVerification, reason string, refundable bool) ports.DomainVerificationInfo {
	return ports.DomainVerificationInfo{DocumentRequired: required, Verification: v, StatusReason: reason, Refundable: refundable}
}

// Sync: mirroring + client emails

func TestSyncMirrorsVerificationState(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(nil))
	stored := captureUpdates(d)
	mails := recordMail(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: verification(true, domain.VerificationDocumentValidating, "", false),
	})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	require.Len(t, *stored, 1)
	got := (*stored)[0]
	assert.Equal(t, domain.VerificationDocumentValidating, got.Verification)
	assert.True(t, got.DocumentRequired)
	assert.Nil(t, got.DocumentRejectedAt)
	assert.Empty(t, *mails, "an upload moving into review needs no email")
}

func TestSyncDocumentReviewBounced(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(func(x *domain.Domain) { x.Verification = domain.VerificationDocumentValidating }))
	stored := captureUpdates(d)
	mails := recordMail(d)
	stubClientUser(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: verification(true, domain.VerificationWaiting, "KTP tidak terbaca", false),
	})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	got := (*stored)[0]
	assert.Equal(t, domain.VerificationWaiting, got.Verification)
	require.NotNil(t, got.DocumentRejectedAt)
	assert.Equal(t, fixedNow, *got.DocumentRejectedAt)
	assert.Equal(t, "KTP tidak terbaca", got.StatusReason)

	require.Len(t, *mails, 1)
	m := (*mails)[0]
	assert.EqualValues(t, 77, m.userID)
	assert.Equal(t, "domain_documents_rejected", m.key)
	assert.Equal(t, "toko.co.id", m.data["Domain"])
	assert.Equal(t, "KTP tidak terbaca", m.data["Reason"])
	// the documented timeline: registered 2026-07-01 -> upload by day 3,
	// suspended day 5, deleted day 30
	assert.Equal(t, "2026-07-03", m.data["UploadBy"])
	assert.Equal(t, "2026-07-05", m.data["SuspendOn"])
	assert.Equal(t, "2026-07-30", m.data["DeleteOn"])
}

func TestSyncReuploadClearsRejection(t *testing.T) {
	rejectedAt := fixedNow.Add(-time.Hour)
	d := newFixture()
	stubGet(d, docDomain(func(x *domain.Domain) { x.DocumentRejectedAt = &rejectedAt }))
	stored := captureUpdates(d)
	mails := recordMail(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: verification(true, domain.VerificationDocumentValidating, "", false),
	})
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	assert.Nil(t, (*stored)[0].DocumentRejectedAt)
	assert.Empty(t, *mails)
}

func TestSyncWaitingAfterWaitingIsNotARejection(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(nil)) // already waiting
	mails := recordMail(d)
	stored := captureUpdates(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: verification(true, domain.VerificationWaiting, "", false),
	})
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	assert.Empty(t, *mails, "no review happened between the two syncs")
	assert.Nil(t, (*stored)[0].DocumentRejectedAt)
}

func TestSyncVerifiedUnsuspendsAndClears(t *testing.T) {
	rejectedAt := fixedNow.Add(-time.Hour)
	d := newFixture()
	stubGet(d, docDomain(func(x *domain.Domain) {
		x.Status = domain.DomainSuspended
		x.DocumentRejectedAt = &rejectedAt
		x.StatusReason = "Document not uploaded"
	}))
	stored := captureUpdates(d)
	mails := recordMail(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: verification(true, domain.VerificationActive, "", false),
	})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	got := (*stored)[0]
	assert.Equal(t, domain.DomainActive, got.Status)
	assert.Equal(t, domain.VerificationActive, got.Verification)
	assert.Nil(t, got.DocumentRejectedAt)
	assert.Empty(t, got.StatusReason)
	assert.Empty(t, *mails)
	assert.Nil(t, got.Deadlines(), "a verified domain no longer counts down")
}

func TestSyncKeepsKnownVerificationWhenRegistrarOmitsIt(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(func(x *domain.Domain) { x.Verification = domain.VerificationDocumentValidating }))
	stored := captureUpdates(d)
	mails := recordMail(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: ports.DomainVerificationInfo{DocumentRequired: true},
	})
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	assert.Equal(t, domain.VerificationDocumentValidating, (*stored)[0].Verification)
	assert.Empty(t, *mails)
}

func TestSyncSuspension(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(nil))
	stored := captureUpdates(d)
	mails := recordMail(d)
	stubClientUser(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "Suspended", DomainVerificationInfo: verification(true, domain.VerificationWaiting, "Document not uploaded", false),
	})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	assert.Equal(t, domain.DomainSuspended, (*stored)[0].Status)
	require.Len(t, *mails, 1)
	assert.Equal(t, "domain_suspended", (*mails)[0].key)
	assert.Equal(t, "Document not uploaded", (*mails)[0].data["Reason"])
	assert.Equal(t, "2026-07-30", (*mails)[0].data["DeleteOn"])
}

func TestSyncSuspensionOfPlainDomainMailsWithoutDeadlines(t *testing.T) {
	d := newFixture()
	stubGet(d, testDomain(nil))
	mails := recordMail(d)
	stubClientUser(d)
	captureUpdates(d)
	syncReturns(d, ports.DomainSyncInfo{Status: "suspended", DomainVerificationInfo: verification(false, domain.VerificationNone, "Abuse report", false)})
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	require.Len(t, *mails, 1)
	assert.NotContains(t, (*mails)[0].data, "DeleteOn")
	assert.NotContains(t, (*mails)[0].data, "SuspendOn")
}

func TestSyncEventMailFailuresAreNotFatal(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(func(x *domain.Domain) { x.Verification = domain.VerificationDocumentValidating }))
	captureUpdates(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "suspended", DomainVerificationInfo: verification(true, domain.VerificationWaiting, "", false),
	})

	t.Run("client user lookup fails", func(t *testing.T) {
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
		require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	})
	t.Run("mail send fails", func(t *testing.T) {
		stubClientUser(d)
		d.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error { return errBoom }
		require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	})
}

func TestSyncPendingBecomesActiveStartsTheClock(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(func(x *domain.Domain) {
		x.Status = domain.DomainPending
		x.RegistrationDate = nil
		x.RegistrarMeta = json.RawMessage(`{"order_id":"77"}`)
	}))
	stored := captureUpdates(d)
	syncReturns(d, ports.DomainSyncInfo{
		Status: "active", DomainVerificationInfo: verification(true, domain.VerificationWaiting, "", false),
	})
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	got := (*stored)[0]
	assert.Equal(t, domain.DomainActive, got.Status)
	require.NotNil(t, got.RegistrationDate)
	assert.Equal(t, fixedNow, *got.RegistrationDate)
}

// Sync: rejection + refund policy

// rejectedFixture is a domain the registrar is about to reject, with a paid
// order behind it.
func rejectedFixture(over func(*domain.Domain)) (*deps, *[]*domain.Domain, *[]mail, *[]string) {
	d := newFixture()
	stubGet(d, testDomain(over))
	stubClientUser(d)
	stored := captureUpdates(d)
	mails := recordMail(d)
	alerts := recordAlerts(d)
	d.payments.RegistrationPaymentFn = func(ctx context.Context, domainID int64) (*ports.DomainRegistrationPayment, error) {
		return &ports.DomainRegistrationPayment{InvoiceID: 900, InvoiceNumber: "INV-900", Amount: 166500}, nil
	}
	return d, stored, mails, alerts
}

func TestSyncRegistrarRejectedAndRefundable_RefundsTheClient(t *testing.T) {
	d, stored, mails, alerts := rejectedFixture(nil)
	var credited struct {
		clientID, delta int64
		reason          string
		invoiceID       *int64
	}
	d.clients.AdjustCreditFn = func(ctx context.Context, clientID, delta int64, reason string, inv *int64) (int64, error) {
		credited.clientID, credited.delta, credited.reason, credited.invoiceID = clientID, delta, reason, inv
		return delta, nil
	}
	syncReturns(d, ports.DomainSyncInfo{
		Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "Registry rejected the registrant", true),
	})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	assert.EqualValues(t, 5, credited.clientID)
	assert.EqualValues(t, 166500, credited.delta)
	assert.Equal(t, "Domain registration refund: example.com", credited.reason)
	require.NotNil(t, credited.invoiceID)
	assert.EqualValues(t, 900, *credited.invoiceID)

	require.Len(t, *stored, 1)
	got := (*stored)[0]
	assert.Equal(t, domain.DomainRejected, got.Status)
	assert.EqualValues(t, 166500, got.RefundAmount)
	require.NotNil(t, got.RefundedAt)
	assert.Equal(t, fixedNow, *got.RefundedAt)
	assert.Equal(t, "Registry rejected the registrant", got.StatusReason)

	require.Len(t, *mails, 1)
	m := (*mails)[0]
	assert.Equal(t, "domain_rejected", m.key)
	assert.Equal(t, true, m.data["Refunded"])
	assert.Equal(t, domain.FormatIDR(166500), m.data["RefundAmount"])
	assert.Equal(t, false, m.data["DocumentFailure"])
	assert.Empty(t, *alerts, "a refunded rejection needs no admin follow-up")
}

func TestSyncDocumentFailureDeletedByRegistrar_NoRefund(t *testing.T) {
	// docs: "Hari ke-30 domain dihapus dan pengembalian dana tidak berlaku
	// apabila pelanggan tidak mengunggah dokumen persyaratan".
	for _, status := range []string{"Deleted", "Pending Delete", "Rejected"} {
		t.Run(status, func(t *testing.T) {
			d, stored, mails, alerts := rejectedFixture(func(x *domain.Domain) {
				*x = *docDomain(func(y *domain.Domain) { y.Status = domain.DomainSuspended })
				x.ID = 10
			})
			d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
				t.Fatal("documents were never verified: the client must not be credited")
				return 0, nil
			}
			syncReturns(d, ports.DomainSyncInfo{
				Status: status,
				// the registrar even flags it refundable - the documented policy wins
				DomainVerificationInfo: verification(true, domain.VerificationWaiting, "Document not uploaded", true),
			})

			require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

			got := (*stored)[0]
			assert.Equal(t, domain.DomainRejected, got.Status)
			assert.Zero(t, got.RefundAmount)
			assert.Nil(t, got.RefundedAt)

			require.Len(t, *mails, 1)
			assert.Equal(t, "domain_rejected", (*mails)[0].key)
			assert.Equal(t, false, (*mails)[0].data["Refunded"])
			assert.Equal(t, true, (*mails)[0].data["DocumentFailure"])

			require.Len(t, *alerts, 1)
			assert.Contains(t, (*alerts)[0], "required documents were never verified")
			assert.Contains(t, (*alerts)[0], "toko.co.id")
		})
	}
}

func TestSyncRejectedButRegistrarNonRefundable_NoRefundAndAlert(t *testing.T) {
	d, stored, mails, alerts := rejectedFixture(nil)
	d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
		t.Fatal("non-refundable: no credit")
		return 0, nil
	}
	syncReturns(d, ports.DomainSyncInfo{Status: "rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", false)})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

	assert.Equal(t, domain.DomainRejected, (*stored)[0].Status)
	assert.Equal(t, false, (*mails)[0].data["Refunded"])
	assert.Equal(t, false, (*mails)[0].data["DocumentFailure"])
	require.Len(t, *alerts, 1)
	assert.Contains(t, (*alerts)[0], "non-refundable")
}

func TestSyncRejectedRefundableWithoutPaidOrder_AlertsInsteadOfGuessing(t *testing.T) {
	for name, fn := range map[string]func(context.Context, int64) (*ports.DomainRegistrationPayment, error){
		"not found": func(context.Context, int64) (*ports.DomainRegistrationPayment, error) {
			return nil, apperr.NotFound("domain payment")
		},
		"zero amount": func(context.Context, int64) (*ports.DomainRegistrationPayment, error) {
			return &ports.DomainRegistrationPayment{Amount: 0}, nil
		},
		"nil": func(context.Context, int64) (*ports.DomainRegistrationPayment, error) { return nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			d, stored, mails, alerts := rejectedFixture(nil)
			d.payments.RegistrationPaymentFn = fn
			d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
				t.Fatal("no paid order: nothing to credit")
				return 0, nil
			}
			syncReturns(d, ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", true)})

			require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))

			assert.Equal(t, domain.DomainRejected, (*stored)[0].Status)
			assert.Nil(t, (*stored)[0].RefundedAt)
			assert.Equal(t, false, (*mails)[0].data["Refunded"])
			require.Len(t, *alerts, 1)
			assert.Contains(t, (*alerts)[0], "no paid order was found")
		})
	}
}

func TestSyncRejection_FailuresLeaveTheDomainUntouched(t *testing.T) {
	info := ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", true)}

	t.Run("payment lookup fails", func(t *testing.T) {
		d, stored, mails, _ := rejectedFixture(nil)
		d.payments.RegistrationPaymentFn = func(context.Context, int64) (*ports.DomainRegistrationPayment, error) { return nil, errBoom }
		syncReturns(d, info)
		require.Error(t, d.svc().SyncDomainJob(context.Background(), 10))
		assert.Empty(t, *stored, "nothing persisted: the next sync retries the whole settlement")
		assert.Empty(t, *mails)
	})

	t.Run("credit fails", func(t *testing.T) {
		d, stored, mails, _ := rejectedFixture(nil)
		d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) { return 0, errBoom }
		syncReturns(d, info)
		require.Error(t, d.svc().SyncDomainJob(context.Background(), 10))
		assert.Empty(t, *stored)
		assert.Empty(t, *mails)
	})

	t.Run("update fails", func(t *testing.T) {
		d, _, mails, _ := rejectedFixture(nil)
		d.domains.UpdateFn = func(context.Context, *domain.Domain) error { return errBoom }
		syncReturns(d, info)
		require.Error(t, d.svc().SyncDomainJob(context.Background(), 10))
		assert.Empty(t, *mails, "no email for a settlement that did not commit")
	})

	t.Run("row lock fails", func(t *testing.T) {
		d, _, _, _ := rejectedFixture(nil)
		d.domains.GetByIDForUpdateFn = func(context.Context, int64) (*domain.Domain, error) { return nil, errBoom }
		syncReturns(d, info)
		require.Error(t, d.svc().SyncDomainJob(context.Background(), 10))
	})
}

func TestSyncRejection_ConcurrentSyncCannotRefundTwice(t *testing.T) {
	// The first read still sees the domain active, but by the time the row is
	// locked another sync has already settled (and refunded) it.
	d, stored, mails, alerts := rejectedFixture(nil)
	settled := testDomain(func(x *domain.Domain) {
		x.Status = domain.DomainRejected
		refunded := fixedNow
		x.RefundedAt = &refunded
		x.RefundAmount = 166500
	})
	d.domains.GetByIDForUpdateFn = func(context.Context, int64) (*domain.Domain, error) { return settled, nil }
	d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
		t.Fatal("a second refund must never be issued")
		return 0, nil
	}
	syncReturns(d, ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", true)})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	assert.Empty(t, *stored)
	assert.Empty(t, *mails)
	assert.Empty(t, *alerts)
}

func TestSyncRejection_RowMovedUnderUsFallsBackToPlainSync(t *testing.T) {
	// Locked row is expired by now: expired -> rejected is not a legal move, so
	// the sync result is applied as an ordinary update with no refund.
	d, stored, mails, _ := rejectedFixture(nil)
	d.domains.GetByIDForUpdateFn = func(context.Context, int64) (*domain.Domain, error) {
		return testDomain(func(x *domain.Domain) { x.Status = domain.DomainExpired }), nil
	}
	d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
		t.Fatal("no refund for a move that never happened")
		return 0, nil
	}
	syncReturns(d, ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", true)})

	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	require.Len(t, *stored, 1)
	assert.Equal(t, domain.DomainExpired, (*stored)[0].Status)
	assert.Empty(t, *mails)
}

func TestSyncRejection_PendingDomainNeverActivated(t *testing.T) {
	// Auto Activation off: the registrar held the domain back, then rejected it.
	d, stored, _, _ := rejectedFixture(func(x *domain.Domain) {
		x.Status = domain.DomainPending
		x.Name = "toko.co.id"
		x.DocumentRequired = true
		x.Verification = domain.VerificationDocumentValidating
	})
	syncReturns(d, ports.DomainSyncInfo{
		Status: "Rejected", DomainVerificationInfo: verification(true, domain.VerificationDocumentValidating, "Rejected by admin", true),
	})
	d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
		t.Fatal("documents never verified: no refund")
		return 0, nil
	}
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	assert.Equal(t, domain.DomainRejected, (*stored)[0].Status)
}

func TestSyncRejection_ClientUserMissingStillSettles(t *testing.T) {
	d, stored, mails, alerts := rejectedFixture(nil)
	d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
	d.clients.AdjustCreditFn = func(ctx context.Context, clientID, delta int64, reason string, inv *int64) (int64, error) {
		return delta, nil
	}
	syncReturns(d, ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", true)})
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	assert.Equal(t, domain.DomainRejected, (*stored)[0].Status)
	assert.Empty(t, *mails)
	assert.Empty(t, *alerts)

	t.Run("admin alert failure is only logged", func(t *testing.T) {
		d, _, _, _ := rejectedFixture(nil)
		d.notifier.AlertAdminFn = func(context.Context, string, string) error { return errBoom }
		syncReturns(d, ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", false)})
		require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	})
}

func TestSyncRejectedStatusIsTerminal(t *testing.T) {
	d, stored, mails, _ := rejectedFixture(func(x *domain.Domain) { x.Status = domain.DomainRejected })
	syncReturns(d, ports.DomainSyncInfo{Status: "Rejected", DomainVerificationInfo: verification(false, domain.VerificationNone, "", true)})
	d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
		t.Fatal("already rejected: never refunded by a repeat sync")
		return 0, nil
	}
	require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
	require.Len(t, *stored, 1)
	assert.Equal(t, domain.DomainRejected, (*stored)[0].Status)
	assert.Empty(t, *mails)
}

// SyncAwaitingVerification

func TestSyncAwaitingVerification(t *testing.T) {
	d := newFixture()
	a := docDomain(func(x *domain.Domain) { x.ID = 1; x.Name = "a.co.id" })
	b := docDomain(func(x *domain.Domain) { x.ID = 2; x.Name = "b.co.id" })
	d.domains.ListAwaitingVerificationFn = func(ctx context.Context, limit int) ([]domain.Domain, error) {
		assert.Positive(t, limit)
		return []domain.Domain{*a, *b}, nil
	}
	d.domains.GetByIDFn = func(ctx context.Context, id int64) (*domain.Domain, error) {
		if id == 1 {
			return a, nil
		}
		return b, nil
	}
	captureUpdates(d)
	d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
		if name == "a.co.id" {
			return nil, apperr.External("rdash", errBoom) // skipped, not fatal
		}
		return &ports.DomainSyncInfo{Status: "active", DomainVerificationInfo: verification(true, domain.VerificationActive, "", false)}, nil
	}
	n, err := d.svc().SyncAwaitingVerification(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	d.domains.ListAwaitingVerificationFn = func(context.Context, int) ([]domain.Domain, error) { return nil, errBoom }
	_, err = d.svc().SyncAwaitingVerification(context.Background())
	assert.Error(t, err)
}

// Register / transfer of a document-requiring domain

func pendingDocDomain() *domain.Domain {
	return testDomain(func(x *domain.Domain) {
		x.Name = "toko.co.id"
		x.Status = domain.DomainPending
		x.ExpiryDate, x.NextDueDate = nil, nil
	})
}

func TestRegisterDocumentRequiredDomain_ActiveAwaitingDocuments(t *testing.T) {
	d := newFixture()
	stubGet(d, pendingDocDomain())
	stubClientUser(d)
	stored := captureUpdates(d)
	mails := recordMail(d)
	d.registrar.RegisterFn = func(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
		return &ports.DomainResult{
			Name: req.Name, Status: "Active", OrderID: "269", ExpiryDate: time.Date(2027, 7, 3, 0, 0, 0, 0, time.UTC),
			DomainVerificationInfo: verification(true, domain.VerificationWaiting, "", false),
		}, nil
	}

	require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))

	got := (*stored)[0]
	assert.Equal(t, domain.DomainActive, got.Status, "RDash Auto Activation: active first, documents afterwards")
	assert.True(t, got.DocumentRequired)
	assert.Equal(t, domain.VerificationWaiting, got.Verification)
	require.NotNil(t, got.RegistrationDate)
	require.Len(t, *mails, 1)
	m := (*mails)[0]
	assert.Equal(t, "domain_documents_required", m.key, "documents instructions replace the plain 'registered' mail")
	// registered "now" (2026-07-03) is day 1: upload through day 3, suspend on
	// day 5, delete on day 30
	assert.Equal(t, "2026-07-05", m.data["UploadBy"])
	assert.Equal(t, "2026-07-07", m.data["SuspendOn"])
	assert.Equal(t, "2026-08-01", m.data["DeleteOn"])
}

func TestRegisterDocumentRequiredDomain_HeldByRegistrar(t *testing.T) {
	// Auto Activation OFF: "domain tidak langsung aktif. Domain dapat aktif
	// ketika sudah lolos verifikasi oleh admin DEWABIZ".
	d := newFixture()
	dom := pendingDocDomain()
	stubGet(d, dom)
	stubClientUser(d)
	stored := captureUpdates(d)
	mails := recordMail(d)
	registers := 0
	d.registrar.RegisterFn = func(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
		registers++
		return &ports.DomainResult{
			Name: req.Name, Status: "Pending", OrderID: "269",
			DomainVerificationInfo: verification(true, domain.VerificationWaiting, "", false),
		}, nil
	}

	require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))
	got := (*stored)[0]
	assert.Equal(t, domain.DomainPending, got.Status, "stays pending until the registrar activates it")
	assert.Nil(t, got.RegistrationDate, "the activation clock has not started")
	assert.JSONEq(t, `{"order_id":"269"}`, string(got.RegistrarMeta))
	require.Len(t, *mails, 1)
	assert.Equal(t, "domain_documents_required", (*mails)[0].key)

	// A retried job must not place the order a second time.
	dom.Status, dom.RegistrarMeta = got.Status, got.RegistrarMeta
	require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))
	assert.Equal(t, 1, registers)
	assert.Len(t, *mails, 1)
}

func TestRegisterVerifiedDocumentDomainGetsTheNormalMail(t *testing.T) {
	d := newFixture()
	stubGet(d, pendingDocDomain())
	stubClientUser(d)
	captureUpdates(d)
	mails := recordMail(d)
	d.registrar.RegisterFn = func(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
		return &ports.DomainResult{Name: req.Name, Status: "Active", DomainVerificationInfo: verification(true, domain.VerificationActive, "", false)}, nil
	}
	require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))
	assert.Equal(t, "domain_registered", (*mails)[0].key)
}

func TestTransferDocumentRequiredDomain(t *testing.T) {
	newDom := func() *domain.Domain {
		x := pendingDocDomain()
		x.Status = domain.DomainPendingTransfer
		x.EPPCodeEnc = "enc"
		return x
	}

	t.Run("stays pending_transfer until the documents verify", func(t *testing.T) {
		d := newFixture()
		dom := newDom()
		stubGet(d, dom)
		stubClientUser(d)
		stored := captureUpdates(d)
		mails := recordMail(d)
		d.encryptor.DecryptFn = func(string) (string, error) { return "EPP", nil }
		transfers := 0
		d.registrar.TransferFn = func(ctx context.Context, req ports.TransferDomainRequest) (*ports.DomainResult, error) {
			transfers++
			return &ports.DomainResult{
				Name: req.Name, Status: "Pending Transfer", OrderID: "300",
				DomainVerificationInfo: verification(true, domain.VerificationWaiting, "", false),
			}, nil
		}
		require.NoError(t, d.svc().TransferDomainJob(context.Background(), 10))
		got := (*stored)[0]
		assert.Equal(t, domain.DomainPendingTransfer, got.Status)
		assert.Nil(t, got.RegistrationDate)
		assert.Equal(t, "domain_documents_required", (*mails)[0].key)

		dom.RegistrarMeta = got.RegistrarMeta
		require.NoError(t, d.svc().TransferDomainJob(context.Background(), 10))
		assert.Equal(t, 1, transfers, "a retried job must not submit the transfer twice")
	})

	t.Run("without documents a transfer completes as before", func(t *testing.T) {
		d := newFixture()
		x := newDom()
		x.Name = "plain.com"
		stubGet(d, x)
		stubClientUser(d)
		stored := captureUpdates(d)
		d.encryptor.DecryptFn = func(string) (string, error) { return "EPP", nil }
		d.registrar.TransferFn = func(ctx context.Context, req ports.TransferDomainRequest) (*ports.DomainResult, error) {
			return &ports.DomainResult{Name: req.Name, Status: "Pending Transfer", OrderID: "301"}, nil
		}
		require.NoError(t, d.svc().TransferDomainJob(context.Background(), 10))
		assert.Equal(t, domain.DomainActive, (*stored)[0].Status)
	})
}

// Availability

func TestCheckAvailabilityFlagsDocumentTLDs(t *testing.T) {
	d := newFixture()
	d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
		out := make([]ports.DomainAvailability, 0, len(names))
		for _, n := range names {
			out = append(out, ports.DomainAvailability{Name: n, Available: true})
		}
		return out, nil
	}
	res, err := d.svc().CheckAvailability(context.Background(), []string{"toko.co.id", "toko.my.id", "toko.com", "sekolah.sch.id"})
	require.NoError(t, err)
	got := map[string]bool{}
	for _, r := range res {
		got[r.Name] = r.DocumentRequired
	}
	assert.Equal(t, map[string]bool{"toko.co.id": true, "toko.my.id": false, "toko.com": false, "sekolah.sch.id": true}, got)
}

// Client: upload link, resend, refresh

func TestDocumentUploadLink(t *testing.T) {
	const link = "https://registrar.example/documents/upload/secret"
	newSvc := func(over func(*domain.Domain)) *deps {
		d := newFixture()
		stubGet(d, docDomain(over))
		d.registrar.DocumentUploadLinkFn = func(ctx context.Context, name string) (string, error) {
			assert.Equal(t, "toko.co.id", name)
			return link, nil
		}
		return d
	}

	t.Run("owner gets the link; audit never carries it", func(t *testing.T) {
		d := newSvc(nil)
		got, err := d.svc().DocumentUploadLink(context.Background(), 5, 10)
		require.NoError(t, err)
		assert.Equal(t, link, got)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.document_link", d.audit.Entries[0].Action)
		raw, _ := json.Marshal(d.audit.Entries[0])
		assert.NotContains(t, string(raw), "secret")
	})

	for _, status := range []domain.DomainStatus{domain.DomainActive, domain.DomainSuspended, domain.DomainPendingTransfer} {
		t.Run("allowed while "+string(status), func(t *testing.T) {
			d := newSvc(func(x *domain.Domain) { x.Status = status })
			_, err := d.svc().DocumentUploadLink(context.Background(), 5, 10)
			require.NoError(t, err)
		})
	}

	t.Run("a pending domain already placed at the registrar", func(t *testing.T) {
		d := newSvc(func(x *domain.Domain) {
			x.Status = domain.DomainPending
			x.RegistrarMeta = json.RawMessage(`{"order_id":"1"}`)
		})
		_, err := d.svc().DocumentUploadLink(context.Background(), 5, 10)
		require.NoError(t, err)
	})

	t.Run("refused", func(t *testing.T) {
		for name, over := range map[string]func(*domain.Domain){
			"no documents required":         func(x *domain.Domain) { x.DocumentRequired = false },
			"already verified":              func(x *domain.Domain) { x.Verification = domain.VerificationActive },
			"pending, not yet at registrar": func(x *domain.Domain) { x.Status = domain.DomainPending },
			"rejected":                      func(x *domain.Domain) { x.Status = domain.DomainRejected },
			"expired":                       func(x *domain.Domain) { x.Status = domain.DomainExpired },
		} {
			d := newSvc(over)
			_, err := d.svc().DocumentUploadLink(context.Background(), 5, 10)
			assertCode(t, err, apperr.CodeConflict)
			assert.Empty(t, d.audit.Entries, name)
		}
	})

	t.Run("another client's domain is NOT_FOUND, never forbidden", func(t *testing.T) {
		d := newSvc(nil)
		_, err := d.svc().DocumentUploadLink(context.Background(), 999, 10)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("unknown domain", func(t *testing.T) {
		d := newFixture()
		stubGet(d, nil)
		_, err := d.svc().DocumentUploadLink(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("registrar error propagates", func(t *testing.T) {
		d := newSvc(nil)
		d.registrar.DocumentUploadLinkFn = func(context.Context, string) (string, error) { return "", apperr.External("rdash", errBoom) }
		_, err := d.svc().DocumentUploadLink(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeExternal)
		assert.Empty(t, d.audit.Entries)
	})
}

func TestResendVerification(t *testing.T) {
	emailDomain := func(over func(*domain.Domain)) *deps {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Name = "toko.my.id"
			x.Verification = domain.VerificationWaiting
			if over != nil {
				over(x)
			}
		}))
		return d
	}

	t.Run("ok", func(t *testing.T) {
		d := emailDomain(nil)
		var resent string
		d.registrar.ResendVerificationFn = func(ctx context.Context, name string) error { resent = name; return nil }
		require.NoError(t, d.svc().ResendVerification(context.Background(), 5, 10))
		assert.Equal(t, "toko.my.id", resent)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.verification_resend", d.audit.Entries[0].Action)
	})

	t.Run("suspended domains can still be re-verified", func(t *testing.T) {
		d := emailDomain(func(x *domain.Domain) { x.Status = domain.DomainSuspended })
		require.NoError(t, d.svc().ResendVerification(context.Background(), 5, 10))
	})

	t.Run("refused", func(t *testing.T) {
		for name, over := range map[string]func(*domain.Domain){
			"not an email-verified tld": func(x *domain.Domain) { x.Name = "toko.com" },
			"document tld":              func(x *domain.Domain) { x.Name = "toko.co.id"; x.DocumentRequired = true },
			"already verified":          func(x *domain.Domain) { x.Verification = domain.VerificationActive },
			"expired":                   func(x *domain.Domain) { x.Status = domain.DomainExpired },
		} {
			d := emailDomain(over)
			d.registrar.ResendVerificationFn = func(context.Context, string) error {
				t.Fatalf("%s: the registrar must not be called", name)
				return nil
			}
			assertCode(t, d.svc().ResendVerification(context.Background(), 5, 10), apperr.CodeConflict)
		}
	})

	t.Run("other client / registrar failure", func(t *testing.T) {
		d := emailDomain(nil)
		assertCode(t, d.svc().ResendVerification(context.Background(), 999, 10), apperr.CodeNotFound)
		d.registrar.ResendVerificationFn = func(context.Context, string) error { return apperr.External("rdash", errBoom) }
		assertCode(t, d.svc().ResendVerification(context.Background(), 5, 10), apperr.CodeExternal)
		assert.Empty(t, d.audit.Entries)
	})
}

func TestRefreshForClient(t *testing.T) {
	d := newFixture()
	stubGet(d, docDomain(nil))
	captureUpdates(d)
	syncReturns(d, ports.DomainSyncInfo{Status: "active", DomainVerificationInfo: verification(true, domain.VerificationDocumentValidating, "", false)})

	dom, err := d.svc().RefreshForClient(context.Background(), 5, 10)
	require.NoError(t, err)
	assert.Equal(t, "toko.co.id", dom.Name)

	_, err = d.svc().RefreshForClient(context.Background(), 999, 10)
	assertCode(t, err, apperr.CodeNotFound)

	d.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
		return nil, apperr.External("rdash", errBoom)
	}
	_, err = d.svc().RefreshForClient(context.Background(), 5, 10)
	assertCode(t, err, apperr.CodeExternal)
}

// Admin refund override

func adminRefundFixture(over func(*domain.Domain)) (*deps, *[]*domain.Domain, *[]mail) {
	d := newFixture()
	stubGet(d, testDomain(func(x *domain.Domain) {
		x.Status = domain.DomainRejected
		if over != nil {
			over(x)
		}
	}))
	stubClientUser(d)
	stored := captureUpdates(d)
	mails := recordMail(d)
	d.payments.RegistrationPaymentFn = func(context.Context, int64) (*ports.DomainRegistrationPayment, error) {
		return &ports.DomainRegistrationPayment{InvoiceID: 900, InvoiceNumber: "INV-900", Amount: 166500}, nil
	}
	return d, stored, mails
}

func TestAdminRefund(t *testing.T) {
	t.Run("full refund by default", func(t *testing.T) {
		d, stored, mails := adminRefundFixture(nil)
		var delta int64
		var reason string
		d.clients.AdjustCreditFn = func(ctx context.Context, clientID, dl int64, r string, inv *int64) (int64, error) {
			assert.EqualValues(t, 5, clientID)
			require.NotNil(t, inv)
			assert.EqualValues(t, 900, *inv)
			delta, reason = dl, r
			return dl, nil
		}
		dom, err := d.svc().AdminRefund(context.Background(), 1, 10, domains.AdminRefundRequest{Reason: "goodwill"})
		require.NoError(t, err)
		assert.EqualValues(t, 166500, delta)
		assert.Equal(t, "Domain refund: example.com (goodwill)", reason)
		assert.EqualValues(t, 166500, dom.RefundAmount)
		require.NotNil(t, dom.RefundedAt)
		assert.Equal(t, fixedNow, *dom.RefundedAt)
		require.Len(t, *stored, 1)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.refund", d.audit.Entries[0].Action)
		assert.EqualValues(t, 1, d.audit.Entries[0].ActorUserID)
		require.Len(t, *mails, 1)
		assert.Equal(t, "domain_rejected", (*mails)[0].key)
		assert.Equal(t, true, (*mails)[0].data["Refunded"])
	})

	t.Run("partial refund, cancelled domains qualify, reason optional", func(t *testing.T) {
		d, _, _ := adminRefundFixture(func(x *domain.Domain) { x.Status = domain.DomainCancelled })
		var reason string
		d.clients.AdjustCreditFn = func(ctx context.Context, clientID, dl int64, r string, inv *int64) (int64, error) {
			assert.EqualValues(t, 50000, dl)
			reason = r
			return dl, nil
		}
		amount := int64(50000)
		dom, err := d.svc().AdminRefund(context.Background(), 1, 10, domains.AdminRefundRequest{Amount: &amount})
		require.NoError(t, err)
		assert.EqualValues(t, 50000, dom.RefundAmount)
		assert.Equal(t, "Domain refund: example.com", reason)
	})

	t.Run("guards", func(t *testing.T) {
		tooMuch := int64(166501)
		cases := map[string]struct {
			over func(*domain.Domain)
			pay  func(context.Context, int64) (*ports.DomainRegistrationPayment, error)
			in   domains.AdminRefundRequest
			code apperr.Code
		}{
			"active domain":         {func(x *domain.Domain) { x.Status = domain.DomainActive }, nil, domains.AdminRefundRequest{}, apperr.CodeConflict},
			"already refunded":      {func(x *domain.Domain) { r := fixedNow; x.RefundedAt = &r }, nil, domains.AdminRefundRequest{}, apperr.CodeConflict},
			"exceeds what was paid": {nil, nil, domains.AdminRefundRequest{Amount: &tooMuch}, apperr.CodeValidation},
			"no paid order": {nil, func(context.Context, int64) (*ports.DomainRegistrationPayment, error) {
				return nil, apperr.NotFound("domain payment")
			}, domains.AdminRefundRequest{}, apperr.CodeConflict},
			"zero paid": {nil, func(context.Context, int64) (*ports.DomainRegistrationPayment, error) {
				return &ports.DomainRegistrationPayment{}, nil
			}, domains.AdminRefundRequest{}, apperr.CodeConflict},
			"nil paid":             {nil, func(context.Context, int64) (*ports.DomainRegistrationPayment, error) { return nil, nil }, domains.AdminRefundRequest{}, apperr.CodeConflict},
			"payment lookup fails": {nil, func(context.Context, int64) (*ports.DomainRegistrationPayment, error) { return nil, errBoom }, domains.AdminRefundRequest{}, apperr.CodeInternal},
		}
		for name, tc := range cases {
			d, stored, mails := adminRefundFixture(tc.over)
			if tc.pay != nil {
				d.payments.RegistrationPaymentFn = tc.pay
			}
			d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
				t.Fatalf("%s: no credit may be issued", name)
				return 0, nil
			}
			_, err := d.svc().AdminRefund(context.Background(), 1, 10, tc.in)
			require.Error(t, err, name)
			assert.Equal(t, tc.code, apperr.From(err).Code, name)
			assert.Empty(t, *stored, name)
			assert.Empty(t, *mails, name)
			assert.Empty(t, d.audit.Entries, name)
		}
	})

	t.Run("failures", func(t *testing.T) {
		d, _, _ := adminRefundFixture(nil)
		stubGet(d, nil)
		_, err := d.svc().AdminRefund(context.Background(), 1, 10, domains.AdminRefundRequest{})
		assertCode(t, err, apperr.CodeNotFound)

		d, _, _ = adminRefundFixture(nil)
		d.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) { return 0, errBoom }
		_, err = d.svc().AdminRefund(context.Background(), 1, 10, domains.AdminRefundRequest{})
		require.Error(t, err)
		assert.Empty(t, d.audit.Entries)

		d, _, _ = adminRefundFixture(nil)
		d.domains.UpdateFn = func(context.Context, *domain.Domain) error { return errBoom }
		_, err = d.svc().AdminRefund(context.Background(), 1, 10, domains.AdminRefundRequest{})
		require.Error(t, err)
		assert.Empty(t, d.audit.Entries)
	})

	t.Run("client user missing: refund still stands, no mail", func(t *testing.T) {
		d, _, mails := adminRefundFixture(nil)
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
		_, err := d.svc().AdminRefund(context.Background(), 1, 10, domains.AdminRefundRequest{})
		require.NoError(t, err)
		assert.Empty(t, *mails)
	})
}
