//go:build integration

package notifications_test

import (
	"context"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/modules/notifications"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seeded domain document/verification lifecycle templates (migration
// 000028) must render, in both locales, through the real template engine and
// the global layout - with every branch of their conditionals.
func TestSeededDomainDocumentTemplatesRender(t *testing.T) {
	ctx := context.Background()
	svc := notifications.New(notifications.Deps{
		Templates:   notifications.NewTemplateRepo(testDB(t)),
		Settings:    &mocks.MockSettingsRepo{},
		Clock:       &mocks.MockClock{},
		FrontendURL: "https://app.example",
	})

	base := map[string]any{
		"Name": "Budi", "Domain": "toko.co.id", "DomainID": 42,
		"Reason": "KTP tidak terbaca", "UploadBy": "2026-07-05", "SuspendOn": "2026-07-07", "DeleteOn": "2026-08-01",
	}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	cases := []struct {
		key  string
		data map[string]any
		// substrings that must appear in the rendered HTML and text parts
		want []string
	}{
		{"domain_documents_required", base, []string{"toko.co.id", "2026-07-05", "2026-07-07", "2026-08-01", "https://app.example/domains/42"}},
		{"domain_documents_required", map[string]any{"Name": "Budi", "Domain": "toko.co.id", "DomainID": 42}, []string{"toko.co.id", "https://app.example/domains/42"}},
		{"domain_documents_rejected", base, []string{"KTP tidak terbaca", "2026-08-01", "https://app.example/domains/42"}},
		{"domain_suspended", base, []string{"KTP tidak terbaca", "2026-08-01", "https://app.example/domains/42"}},
		{"domain_rejected", with(map[string]any{"Refunded": true, "RefundAmount": "Rp 166.500", "DocumentFailure": false}), []string{"Rp 166.500", "https://app.example/domains"}},
		{"domain_rejected", with(map[string]any{"Refunded": false, "RefundAmount": "Rp 0", "DocumentFailure": true}), []string{"toko.co.id"}},
		{"domain_rejected", with(map[string]any{"Refunded": false, "RefundAmount": "Rp 0", "DocumentFailure": false}), []string{"toko.co.id"}},
	}
	for _, tc := range cases {
		for _, locale := range []string{"id", "en"} {
			res, err := svc.Preview(ctx, notifications.PreviewInput{Key: tc.key, Locale: locale, SampleData: tc.data})
			require.NoError(t, err, "%s/%s", tc.key, locale)
			assert.Equal(t, locale, res.Locale, "%s: both locales are seeded", tc.key)
			assert.Contains(t, res.Subject, "toko.co.id", "%s/%s subject", tc.key, locale)
			for _, w := range tc.want {
				assert.Contains(t, res.BodyHTML, w, "%s/%s html", tc.key, locale)
				assert.Contains(t, res.BodyText, w, "%s/%s text", tc.key, locale)
			}
			assert.NotContains(t, res.BodyHTML, "<no value>", "%s/%s html has an unfilled variable", tc.key, locale)
			assert.NotContains(t, res.BodyText, "<no value>", "%s/%s text has an unfilled variable", tc.key, locale)
		}
	}

	// the refund outcome copy follows the flags
	refunded, err := svc.Preview(ctx, notifications.PreviewInput{Key: "domain_rejected", Locale: "en",
		SampleData: with(map[string]any{"Refunded": true, "RefundAmount": "Rp 166.500", "DocumentFailure": false})})
	require.NoError(t, err)
	assert.Contains(t, refunded.BodyText, "returned to your account credit")

	docFailure, err := svc.Preview(ctx, notifications.PreviewInput{Key: "domain_rejected", Locale: "en",
		SampleData: with(map[string]any{"Refunded": false, "RefundAmount": "Rp 0", "DocumentFailure": true})})
	require.NoError(t, err)
	assert.Contains(t, docFailure.BodyText, "no refund")
	assert.NotContains(t, docFailure.BodyText, "returned to your account credit")

	nonRefundable, err := svc.Preview(ctx, notifications.PreviewInput{Key: "domain_rejected", Locale: "en",
		SampleData: with(map[string]any{"Refunded": false, "RefundAmount": "Rp 0", "DocumentFailure": false})})
	require.NoError(t, err)
	assert.Contains(t, nonRefundable.BodyText, "non-refundable")
}
