package billing

// Failure paths of the billing Service: every repo/settings/port failure
// must map to a sensible apperr and never partially apply work.

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

func TestCreateInvoiceSettingsErrors(t *testing.T) {
	base := ports.CreateInvoiceInput{
		ClientID: 1,
		Items:    []ports.CreateInvoiceItem{{Description: "x", Amount: 1000, Taxed: true}},
	}

	t.Run("due days lookup fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errBoom }
		_, err := env.service().CreateInvoice(context.Background(), base)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("tax enabled lookup fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return false, errBoom }
		in := base
		in.DueDate = date(2026, 8, 1)
		_, err := env.service().CreateInvoice(context.Background(), in)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("tax rate lookup fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) { return true, nil }
		env.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errBoom }
		in := base
		in.DueDate = date(2026, 8, 1)
		_, err := env.service().CreateInvoice(context.Background(), in)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("tax inclusive lookup fails", func(t *testing.T) {
		env := newFixture()
		calls := 0
		env.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			calls++
			if calls == 1 {
				return true, nil // tax_enabled
			}
			return false, errBoom // tax_inclusive
		}
		in := base
		in.DueDate = date(2026, 8, 1)
		_, err := env.service().CreateInvoice(context.Background(), in)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("next number fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.NextNumberFn = func(context.Context, string) (int64, error) { return 0, errBoom }
		in := base
		in.DueDate = date(2026, 8, 1)
		_, err := env.service().CreateInvoice(context.Background(), in)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})
}

func TestProcessPaidErrorBranches(t *testing.T) {
	items := func(its ...domain.InvoiceItem) func(context.Context, int64) ([]domain.InvoiceItem, error) {
		return func(context.Context, int64) ([]domain.InvoiceItem, error) { return its, nil }
	}

	t.Run("invoice lookup fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(context.Context, int64) (*domain.Invoice, error) {
			return nil, apperr.NotFound("invoice")
		}
		assert.Equal(t, apperr.CodeNotFound, codeOf(t, env.service().ProcessPaid(context.Background(), 1)))
	})

	t.Run("items lookup fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) { return nil, errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1))
	})

	t.Run("order lookup fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(domain.InvoiceItem{RelatedType: domain.RelatedOrderItem, RelatedID: rid(9)})
		env.invoices.OrderIDForOrderItemFn = func(context.Context, int64) (int64, error) { return 0, errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1))
	})

	t.Run("renew fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(domain.InvoiceItem{RelatedType: domain.RelatedServiceRenewal, RelatedID: rid(9)})
		env.renewer.RenewServiceFn = func(context.Context, int64) error { return errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1))
	})

	t.Run("upgrade fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(domain.InvoiceItem{RelatedType: domain.RelatedServiceUpgrade, RelatedID: rid(9)})
		env.renewer.ApplyUpgradeFn = func(context.Context, int64) error { return errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1))
	})

	t.Run("domain renew fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(domain.InvoiceItem{RelatedType: domain.RelatedDomainRenewal, RelatedID: rid(9)})
		env.domRenewer.RenewDomainAfterPaymentFn = func(context.Context, int64) error { return errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1))
	})

	t.Run("snipe activation fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(domain.InvoiceItem{RelatedType: domain.RelatedDomainSnipe, RelatedID: rid(9)})
		env.snipes.ActivateSnipeAfterPaymentFn = func(context.Context, int64) error { return errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1), "the payment must not commit without the hunt starting")
	})

	t.Run("deposit credit fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(domain.InvoiceItem{RelatedType: domain.RelatedDeposit, Amount: 10_000})
		env.credit.AddCreditFn = func(context.Context, int64, int64, string, int64) error { return errBoom }
		assert.Error(t, env.service().ProcessPaid(context.Background(), 1))
	})

	t.Run("nil related ids are skipped", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) { return paidInvoice(id), nil }
		env.invoices.GetItemsFn = items(
			domain.InvoiceItem{RelatedType: domain.RelatedOrderItem},
			domain.InvoiceItem{RelatedType: domain.RelatedServiceRenewal},
			domain.InvoiceItem{RelatedType: domain.RelatedServiceUpgrade},
			domain.InvoiceItem{RelatedType: domain.RelatedDomainRenewal},
		)
		called := false
		env.activator.ActivateOrderFn = func(context.Context, int64) error { called = true; return nil }
		require.NoError(t, env.service().ProcessPaid(context.Background(), 1))
		assert.False(t, called)
	})
}

func TestGenerateRenewalInvoicesErrorBranches(t *testing.T) {
	due := date(2026, 7, 10)

	t.Run("lead setting fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errBoom }
		_, err := env.service().GenerateRenewalInvoices(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("services list fails", func(t *testing.T) {
		env := newFixture()
		env.services.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Service, error) { return nil, errBoom }
		_, err := env.service().GenerateRenewalInvoices(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("open check fails per entity", func(t *testing.T) {
		env := newFixture()
		svc1 := dueService(1, due)
		env.services.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Service, error) {
			return []domain.Service{svc1}, nil
		}
		env.services.GetByIDForUpdateFn = byServiceID(svc1)
		env.invoices.HasOpenRenewalInvoiceFn = func(context.Context, domain.InvoiceItemRelatedType, int64) (bool, error) {
			return false, errBoom
		}
		n, err := env.service().GenerateRenewalInvoices(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})

	t.Run("domains list fails after services", func(t *testing.T) {
		env := newFixture()
		env.domains.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Domain, error) { return nil, errBoom }
		n, err := env.service().GenerateRenewalInvoices(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})

	t.Run("domain skips and errors", func(t *testing.T) {
		env := newFixture()
		amountless := domain.Domain{ID: 1, ClientID: 3, Name: "a.id", Status: domain.DomainActive, NextDueDate: &due}
		dueless := domain.Domain{ID: 2, ClientID: 3, Name: "b.id", Status: domain.DomainActive, RecurringAmount: 10_000}
		open := domain.Domain{ID: 3, ClientID: 3, Name: "c.id", Status: domain.DomainActive, RecurringAmount: 10_000, NextDueDate: &due}
		failing := domain.Domain{ID: 4, ClientID: 3, Name: "d.id", Status: domain.DomainActive, RecurringAmount: 10_000, NextDueDate: &due}
		env.domains.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Domain, error) {
			return []domain.Domain{amountless, dueless, open, failing}, nil
		}
		env.domains.GetByIDForUpdateFn = byDomainID(amountless, dueless, open, failing)
		env.invoices.HasOpenRenewalInvoiceFn = func(_ context.Context, _ domain.InvoiceItemRelatedType, id int64) (bool, error) {
			if id == 3 {
				return true, nil
			}
			if id == 4 {
				return false, errBoom
			}
			return false, nil
		}
		n, err := env.service().GenerateRenewalInvoices(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})
}

func TestMarkOverdueErrorBranches(t *testing.T) {
	t.Run("list fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			return nil, errBoom
		}
		_, err := env.service().MarkOverdue(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("update fails per invoice", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			return []domain.Invoice{
				{ID: 1, ClientID: 3, Status: domain.InvoiceUnpaid},
				{ID: 2, ClientID: 3, Status: domain.InvoiceUnpaid},
			}, nil
		}
		env.invoices.UpdateStatusFn = func(_ context.Context, id int64, _ domain.InvoiceStatus, _ *time.Time) error {
			if id == 1 {
				return errBoom
			}
			return nil
		}
		n, err := env.service().MarkOverdue(context.Background())
		assert.Equal(t, 1, n)
		assert.Error(t, err)
	})

	t.Run("illegal transition is reported", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			// a paid invoice sneaks in (racy status change)
			return []domain.Invoice{{ID: 1, ClientID: 3, Status: domain.InvoicePaid}}, nil
		}
		n, err := env.service().MarkOverdue(context.Background())
		assert.Zero(t, n)
		assert.Equal(t, apperr.CodeConflict, codeOf(t, err))
	})
}

func TestSendRemindersErrorBranches(t *testing.T) {
	t.Run("reminder days setting fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetJSONFn = func(context.Context, string, any) error { return errBoom }
		_, err := env.service().SendReminders(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("overdue days setting fails", func(t *testing.T) {
		env := newFixture()
		calls := 0
		env.settings.GetJSONFn = func(context.Context, string, any) error {
			calls++
			if calls == 2 {
				return errBoom
			}
			return nil
		}
		_, err := env.service().SendReminders(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("unpaid list fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			return nil, errBoom
		}
		_, err := env.service().SendReminders(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("overdue list fails after unpaid processed", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(_ context.Context, status domain.InvoiceStatus, _ time.Time) ([]domain.Invoice, error) {
			if status == domain.InvoiceOverdue {
				return nil, errBoom
			}
			return []domain.Invoice{{ID: 1, InvoiceNumber: "INV-1", ClientID: 3, DueDate: date(2026, 7, 6)}}, nil
		}
		n, err := env.service().SendReminders(context.Background())
		assert.Equal(t, 1, n)
		assert.Error(t, err)
	})

	t.Run("dedupe check fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(_ context.Context, status domain.InvoiceStatus, _ time.Time) ([]domain.Invoice, error) {
			if status == domain.InvoiceUnpaid {
				return []domain.Invoice{{ID: 1, InvoiceNumber: "INV-1", ClientID: 3, DueDate: date(2026, 7, 6)}}, nil
			}
			return nil, nil
		}
		env.invoices.HasEmailSinceFn = func(context.Context, string, string, time.Time) (bool, error) {
			return false, errBoom
		}
		n, err := env.service().SendReminders(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})

	t.Run("client lookup fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(_ context.Context, status domain.InvoiceStatus, _ time.Time) ([]domain.Invoice, error) {
			if status == domain.InvoiceUnpaid {
				return []domain.Invoice{{ID: 1, InvoiceNumber: "INV-1", ClientID: 3, DueDate: date(2026, 7, 6)}}, nil
			}
			return nil, nil
		}
		env.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
		n, err := env.service().SendReminders(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})

	t.Run("send fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListDueForStatusFn = func(_ context.Context, status domain.InvoiceStatus, _ time.Time) ([]domain.Invoice, error) {
			if status == domain.InvoiceUnpaid {
				return []domain.Invoice{{ID: 1, InvoiceNumber: "INV-1", ClientID: 3, DueDate: date(2026, 7, 6)}}, nil
			}
			return nil, nil
		}
		env.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error { return errBoom }
		n, err := env.service().SendReminders(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})
}

func TestApplyLateFeesErrorBranches(t *testing.T) {
	feeSettings := map[string]any{"billing.late_fee_enabled": true, "billing.late_fee_amount": 5_000}

	t.Run("enabled setting fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return false, errBoom }
		_, err := env.service().ApplyLateFees(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("amount setting fails", func(t *testing.T) {
		env := newFixture()
		env.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return true, nil }
		env.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errBoom }
		_, err := env.service().ApplyLateFees(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("list fails", func(t *testing.T) {
		env := newFixture()
		env.settings = settingsWith(feeSettings)
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			return nil, errBoom
		}
		_, err := env.service().ApplyLateFees(context.Background())
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("items lookup fails per invoice", func(t *testing.T) {
		env := newFixture()
		env.settings = settingsWith(feeSettings)
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			return []domain.Invoice{{ID: 1, ClientID: 3, Status: domain.InvoiceOverdue}}, nil
		}
		env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) { return nil, errBoom }
		n, err := env.service().ApplyLateFees(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})

	t.Run("add item fails inside tx", func(t *testing.T) {
		env := newFixture()
		env.settings = settingsWith(feeSettings)
		env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
			return []domain.Invoice{{ID: 1, ClientID: 3, Status: domain.InvoiceOverdue}}, nil
		}
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoiceOverdue}, nil
		}
		env.invoices.AddItemFn = func(context.Context, *domain.InvoiceItem) error { return errBoom }
		n, err := env.service().ApplyLateFees(context.Background())
		assert.Zero(t, n)
		assert.Error(t, err)
	})
}

func TestGenerateInvoicePDFErrorBranches(t *testing.T) {
	okInvoice := func(env *testEnv, status domain.InvoiceStatus) {
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			inv := paidInvoice(id)
			inv.Status = status
			return inv, nil
		}
	}

	t.Run("invoice not found", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(context.Context, int64) (*domain.Invoice, error) {
			return nil, apperr.NotFound("invoice")
		}
		assert.Equal(t, apperr.CodeNotFound, codeOf(t, env.service().GenerateInvoicePDF(context.Background(), 1)))
	})

	t.Run("items fail", func(t *testing.T) {
		env := newFixture()
		okInvoice(env, domain.InvoicePaid)
		env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) { return nil, errBoom }
		assert.Error(t, env.service().GenerateInvoicePDF(context.Background(), 1))
	})

	t.Run("client fails", func(t *testing.T) {
		env := newFixture()
		okInvoice(env, domain.InvoicePaid)
		env.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
		assert.Error(t, env.service().GenerateInvoicePDF(context.Background(), 1))
	})

	t.Run("user fails", func(t *testing.T) {
		env := newFixture()
		okInvoice(env, domain.InvoicePaid)
		env.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) { return nil, errBoom }
		assert.Error(t, env.service().GenerateInvoicePDF(context.Background(), 1))
	})

	t.Run("proforma setting fails", func(t *testing.T) {
		env := newFixture()
		okInvoice(env, domain.InvoiceUnpaid)
		env.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return false, errBoom }
		assert.Equal(t, apperr.CodeInternal, codeOf(t, env.service().GenerateInvoicePDF(context.Background(), 1)))
	})

	t.Run("render fails", func(t *testing.T) {
		env := newFixture()
		okInvoice(env, domain.InvoicePaid)
		env.pdf.InvoicePDFFn = func(context.Context, ports.InvoicePDFData) ([]byte, error) { return nil, errBoom }
		assert.Equal(t, apperr.CodeInternal, codeOf(t, env.service().GenerateInvoicePDF(context.Background(), 1)))
	})

	t.Run("storage put fails", func(t *testing.T) {
		env := newFixture()
		okInvoice(env, domain.InvoicePaid)
		env.storage.PutFn = func(context.Context, string, io.Reader, int64, string) error { return errBoom }
		assert.Equal(t, apperr.CodeInternal, codeOf(t, env.service().GenerateInvoicePDF(context.Background(), 1)))
	})
}

func TestQueryErrorBranches(t *testing.T) {
	t.Run("list client invoices fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.Invoice, int64, error) {
			return nil, 0, errBoom
		}
		_, _, err := env.service().ListClientInvoices(context.Background(), 3, ports.ListParams{})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("admin list works and fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.ListFn = func(context.Context, ports.ListParams) ([]domain.Invoice, int64, error) {
			return []domain.Invoice{{ID: 1}}, 1, nil
		}
		list, total, err := env.service().AdminListInvoices(context.Background(), ports.ListParams{})
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, int64(1), total)

		env.invoices.ListFn = func(context.Context, ports.ListParams) ([]domain.Invoice, int64, error) {
			return nil, 0, errBoom
		}
		_, _, err = env.service().AdminListInvoices(context.Background(), ports.ListParams{})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("get invoice items fail", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, ClientID: 3}, nil
		}
		env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) { return nil, errBoom }
		_, err := env.service().GetInvoice(context.Background(), 3, 1)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("download pdf generation fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			inv := paidInvoice(id)
			inv.PDFObjectKey = ""
			return inv, nil
		}
		env.pdf.InvoicePDFFn = func(context.Context, ports.InvoicePDFData) ([]byte, error) { return nil, errBoom }
		_, _, err := env.service().DownloadPDF(context.Background(), 3, 1)
		assert.Error(t, err)
	})

	t.Run("download pdf storage get fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			inv := paidInvoice(id)
			inv.PDFObjectKey = "invoices/x.pdf"
			return inv, nil
		}
		env.storage.GetFn = func(context.Context, string) (io.ReadCloser, error) { return nil, errBoom }
		_, _, err := env.service().DownloadPDF(context.Background(), 3, 1)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})
}

func TestAdminOpsErrorBranches(t *testing.T) {
	t.Run("manual invoice create propagates", func(t *testing.T) {
		env := newFixture()
		env.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) {
			return nil, apperr.NotFound("client")
		}
		_, err := env.service().CreateManualInvoice(context.Background(), 42, ManualInvoiceInput{
			ClientID: 3,
			Items:    []ManualInvoiceItemInput{{Description: "x", Amount: 100}},
		})
		assert.Equal(t, apperr.CodeNotFound, codeOf(t, err))
	})

	t.Run("manual payment invoice missing", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(context.Context, int64) (*domain.Invoice, error) {
			return nil, apperr.NotFound("invoice")
		}
		_, err := env.service().AddManualPayment(context.Background(), 42, 1, ManualPaymentInput{Amount: 1, Method: "m"})
		assert.Equal(t, apperr.CodeNotFound, codeOf(t, err))
	})

	t.Run("manual payment apply fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid}, nil
		}
		env.payments.ApplyPaymentFn = func(context.Context, int64, ports.ApplyTx) error {
			return apperr.Conflict("amount too low")
		}
		_, err := env.service().AddManualPayment(context.Background(), 42, 1, ManualPaymentInput{Amount: 1, Method: "m"})
		assert.Equal(t, apperr.CodeConflict, codeOf(t, err))
	})

	t.Run("cancel invoice missing", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(context.Context, int64) (*domain.Invoice, error) {
			return nil, apperr.NotFound("invoice")
		}
		_, err := env.service().CancelInvoice(context.Background(), 42, 1)
		assert.Equal(t, apperr.CodeNotFound, codeOf(t, err))
	})

	t.Run("cancel update fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid}, nil
		}
		env.invoices.UpdateStatusFn = func(context.Context, int64, domain.InvoiceStatus, *time.Time) error { return errBoom }
		_, err := env.service().CancelInvoice(context.Background(), 42, 1)
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("refund item listing fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoicePaid, Total: 100}, nil
		}
		env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) { return nil, errBoom }
		_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("refund tx listing fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoicePaid, Total: 100}, nil
		}
		env.txRepo.ListByInvoiceFn = func(context.Context, int64) ([]domain.Transaction, error) { return nil, errBoom }
		_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("refund tx update fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoicePaid, Total: 100}, nil
		}
		env.txRepo.ListByInvoiceFn = func(context.Context, int64) ([]domain.Transaction, error) {
			return []domain.Transaction{{ID: 1, Status: domain.TxSuccess}}, nil
		}
		env.txRepo.UpdateFn = func(context.Context, *domain.Transaction) error { return errBoom }
		_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("refund credit fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, InvoiceNumber: "INV-R", ClientID: 3,
				Status: domain.InvoicePaid, Total: 100}, nil
		}
		env.credit.AddCreditFn = func(context.Context, int64, int64, string, int64) error { return errBoom }
		_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{ToCredit: true})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})

	t.Run("update invoice repo fails", func(t *testing.T) {
		env := newFixture()
		env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid}, nil
		}
		env.invoices.UpdateFn = func(context.Context, *domain.Invoice) error { return errBoom }
		notes := "x"
		_, err := env.service().UpdateInvoice(context.Background(), 42, 1, UpdateInvoiceInput{Notes: &notes})
		assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
	})
}

func TestNotifyClientNilClient(t *testing.T) {
	env := newFixture()
	env.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, nil }
	sent := false
	env.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error {
		sent = true
		return nil
	}
	env.service().notifyClient(context.Background(), 3, "x", map[string]any{})
	assert.False(t, sent)
}

func TestGetInvoiceLookupError(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(context.Context, int64) (*domain.Invoice, error) {
		return nil, apperr.NotFound("invoice")
	}
	_, err := env.service().GetInvoice(context.Background(), 3, 1)
	assert.Equal(t, apperr.CodeNotFound, codeOf(t, err))
}
