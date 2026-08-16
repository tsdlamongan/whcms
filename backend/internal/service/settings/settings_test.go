package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/internal/service/settings"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seededRepo() *mocks.MockSettingsRepo {
	store := map[string]json.RawMessage{
		"company.name":             json.RawMessage(`"WHCMS"`),
		"billing.tax_rate":         json.RawMessage(`11`),
		"billing.tax_enabled":      json.RawMessage(`false`),
		"billing.reminder_days":    json.RawMessage(`[7,3,1]`),
		"billing.invoice_due_days": json.RawMessage(`3`),
	}
	return &mocks.MockSettingsRepo{
		AllFn: func(ctx context.Context) ([]domain.Setting, error) {
			// Deterministic order: sorted keys, reflecting whatever is
			// currently in store (including keys added via Set), so tests
			// that Update() a new key and then re-read via Grouped() see it.
			keys := make([]string, 0, len(store))
			for k := range store {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]domain.Setting, 0, len(store))
			for _, k := range keys {
				out = append(out, domain.Setting{Key: k, Value: store[k]})
			}
			return out, nil
		},
		SetFn: func(ctx context.Context, key string, value any) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			store[key] = raw
			return nil
		},
	}
}

func TestGrouped(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	got, err := svc.Grouped(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "WHCMS", got["company"]["name"])
	assert.Equal(t, float64(11), got["billing"]["tax_rate"])
	assert.Equal(t, false, got["billing"]["tax_enabled"])
	assert.Equal(t, []any{float64(7), float64(3), float64(1)}, got["billing"]["reminder_days"])
}

func TestTaxConfig(t *testing.T) {
	t.Run("reads effective settings", func(t *testing.T) {
		repo := &mocks.MockSettingsRepo{
			GetBoolFn: func(ctx context.Context, key string, def bool) (bool, error) {
				switch key {
				case "billing.tax_enabled":
					return true, nil
				case "billing.tax_inclusive":
					return true, nil
				}
				return def, nil
			},
			GetJSONFn: func(ctx context.Context, key string, out any) error {
				if key == "billing.tax_rate" {
					*(out.(*float64)) = 8.5
				}
				return nil
			},
		}
		svc := settings.New(repo, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
		got := svc.TaxConfig(context.Background())
		assert.Equal(t, settings.PublicTaxConfig{Enabled: true, Rate: 8.5, Inclusive: true}, got)
	})

	t.Run("defaults when nothing set", func(t *testing.T) {
		svc := settings.New(&mocks.MockSettingsRepo{}, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
		got := svc.TaxConfig(context.Background())
		assert.Equal(t, settings.PublicTaxConfig{Enabled: false, Rate: 11, Inclusive: false}, got)
	})

	t.Run("degrades to defaults on read error instead of failing", func(t *testing.T) {
		repo := &mocks.MockSettingsRepo{
			GetBoolFn: func(ctx context.Context, key string, def bool) (bool, error) {
				return def, errors.New("db down")
			},
			GetJSONFn: func(ctx context.Context, key string, out any) error {
				return errors.New("db down")
			},
		}
		svc := settings.New(repo, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
		got := svc.TaxConfig(context.Background())
		assert.Equal(t, settings.PublicTaxConfig{Enabled: false, Rate: 11, Inclusive: false}, got)
	})
}

func TestRequireEmailVerification(t *testing.T) {
	t.Run("reads the stored setting", func(t *testing.T) {
		repo := &mocks.MockSettingsRepo{
			GetBoolFn: func(ctx context.Context, key string, def bool) (bool, error) {
				if key == "security.require_email_verification" {
					return false, nil
				}
				return def, nil
			},
		}
		svc := settings.New(repo, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
		assert.False(t, svc.RequireEmailVerification(context.Background()))
	})

	t.Run("defaults to true when unset or on read error", func(t *testing.T) {
		svc := settings.New(&mocks.MockSettingsRepo{}, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
		assert.True(t, svc.RequireEmailVerification(context.Background()))

		failing := &mocks.MockSettingsRepo{
			GetBoolFn: func(ctx context.Context, key string, def bool) (bool, error) {
				return def, errors.New("db down")
			},
		}
		svc = settings.New(failing, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
		assert.True(t, svc.RequireEmailVerification(context.Background()))
	})
}

func TestGroupedRepoError(t *testing.T) {
	repo := &mocks.MockSettingsRepo{
		AllFn: func(ctx context.Context) ([]domain.Setting, error) {
			return nil, errors.New("db down")
		},
	}
	svc := settings.New(repo, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	_, err := svc.Grouped(context.Background())
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeInternal, ae.Code)
}

func TestUpdateHappyPath(t *testing.T) {
	repo := seededRepo()
	audit := &mocks.MockAuditLogger{}
	svc := settings.New(repo, &mocks.MockTxManager{}, audit)

	err := svc.Update(context.Background(), 42, map[string]any{
		"company.name":          "Hosting Kita",
		"billing.tax_enabled":   true,
		"billing.tax_rate":      float64(11),
		"billing.reminder_days": []any{float64(7), float64(1)},
	})
	require.NoError(t, err)

	// Persisted.
	got, err := svc.Grouped(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Hosting Kita", got["company"]["name"])
	assert.Equal(t, true, got["billing"]["tax_enabled"])

	// Audited with before/after.
	require.Len(t, audit.Entries, 1)
	entry := audit.Entries[0]
	assert.Equal(t, int64(42), entry.ActorUserID)
	assert.Equal(t, "settings.update", entry.Action)
	before := entry.Before.(map[string]any)
	assert.Equal(t, "WHCMS", before["company.name"])
	after := entry.After.(map[string]any)
	assert.Equal(t, "Hosting Kita", after["company.name"])
}

func TestUpdateRejectsUnknownKey(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	err := svc.Update(context.Background(), 1, map[string]any{"hack.the.planet": true})
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeValidation, ae.Code)
	require.Len(t, ae.Details, 1)
	assert.Equal(t, "hack.the.planet", ae.Details[0].Field)
	assert.Equal(t, "unknown settings key", ae.Details[0].Message)
}

func TestUpdateRejectsWrongTypes(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	tests := []struct {
		key string
		val any
		msg string
	}{
		{"company.name", 42, "must be a string"},
		{"billing.tax_enabled", "yes", "must be a boolean"},
		{"billing.tax_rate", "eleven", "must be a number"},
		{"billing.invoice_due_days", 3.5, "must be an integer"},
		{"billing.reminder_days", []any{float64(7), "x"}, "must be a list of integers"},
		{"billing.reminder_days", "7,3,1", "must be a list of integers"},
		{"tickets.allowed_extensions", []any{1}, "must be a list of strings"},
	}
	for _, tt := range tests {
		err := svc.Update(context.Background(), 1, map[string]any{tt.key: tt.val})
		var ae *apperr.Error
		require.True(t, errors.As(err, &ae), "%s=%v", tt.key, tt.val)
		require.Len(t, ae.Details, 1)
		assert.Equal(t, tt.msg, ae.Details[0].Message, "%s=%v", tt.key, tt.val)
	}
}

func TestUpdateAndReadNewlyWhitelistedKeys(t *testing.T) {
	repo := seededRepo()
	svc := settings.New(repo, &mocks.MockTxManager{}, &mocks.MockAuditLogger{})

	err := svc.Update(context.Background(), 1, map[string]any{
		"fraud.max_orders_per_day":     float64(5),
		"fraud.email_domain_blacklist": []any{"mailinator.com", "example.test"},
		"domains.default_nameservers":  []any{"ns1.example.id", "ns2.example.id"},
		"billing.proforma_enabled":     true,
	})
	require.NoError(t, err)

	got, err := svc.Grouped(context.Background())
	require.NoError(t, err)
	assert.Equal(t, float64(5), got["fraud"]["max_orders_per_day"])
	assert.Equal(t, []any{"mailinator.com", "example.test"}, got["fraud"]["email_domain_blacklist"])
	assert.Equal(t, []any{"ns1.example.id", "ns2.example.id"}, got["domains"]["default_nameservers"])
	assert.Equal(t, true, got["billing"]["proforma_enabled"])
}

func TestUpdateRejectsWrongTypesForNewKeys(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	tests := []struct {
		key string
		val any
		msg string
	}{
		{"fraud.max_orders_per_day", "ten", "must be an integer"},
		{"fraud.email_domain_blacklist", []any{1}, "must be a list of strings"},
		{"domains.default_nameservers", "ns1.example.id", "must be a list of strings"},
		{"billing.proforma_enabled", "yes", "must be a boolean"},
	}
	for _, tt := range tests {
		err := svc.Update(context.Background(), 1, map[string]any{tt.key: tt.val})
		var ae *apperr.Error
		require.True(t, errors.As(err, &ae), "%s=%v", tt.key, tt.val)
		require.Len(t, ae.Details, 1)
		assert.Equal(t, tt.msg, ae.Details[0].Message, "%s=%v", tt.key, tt.val)
	}
}

func TestGatewayDuitkuKeysRemainUnwhitelisted(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	err := svc.Update(context.Background(), 1, map[string]any{"gateway.duitku.merchant_code": "DEMO"})
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeValidation, ae.Code)
	assert.Equal(t, "unknown settings key", ae.Details[0].Message)
}

func TestUpdateAcceptsTypedSlices(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	err := svc.Update(context.Background(), 1, map[string]any{
		"billing.reminder_days":      []int{7, 3, 1},
		"tickets.allowed_extensions": []string{"jpg", "pdf"},
	})
	assert.NoError(t, err)
}

func TestUpdateEmpty(t *testing.T) {
	svc := settings.New(seededRepo(), &mocks.MockTxManager{}, &mocks.MockAuditLogger{})
	err := svc.Update(context.Background(), 1, map[string]any{})
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeValidation, ae.Code)
}

func TestUpdateRunsInsideTx(t *testing.T) {
	repo := seededRepo()
	txCalled := false
	tx := &mocks.MockTxManager{
		WithinTxFn: func(ctx context.Context, fn func(ctx context.Context) error) error {
			txCalled = true
			return fn(ctx)
		},
	}
	svc := settings.New(repo, tx, &mocks.MockAuditLogger{})
	require.NoError(t, svc.Update(context.Background(), 1, map[string]any{"company.name": "X"}))
	assert.True(t, txCalled)
}

func TestUpdateSetFailureIsInternalAndNotAudited(t *testing.T) {
	repo := seededRepo()
	repo.SetFn = func(ctx context.Context, key string, value any) error {
		return errors.New("write failed")
	}
	audit := &mocks.MockAuditLogger{}
	svc := settings.New(repo, &mocks.MockTxManager{}, audit)

	err := svc.Update(context.Background(), 1, map[string]any{"company.name": "X"})
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeInternal, ae.Code)
	assert.Empty(t, audit.Entries, "failed update must not be audited")
}
