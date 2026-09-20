package domain_test

import (
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestProrate(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		cycle  domain.BillingCycle
		from   time.Time
		to     time.Time
		want   int64
	}{
		{
			"half month",
			310000, domain.CycleMonthly,
			date(2026, 7, 1), date(2026, 7, 16), // 15 of 31 days
			150000,
		},
		{
			"full month exact",
			310000, domain.CycleMonthly,
			date(2026, 7, 1), date(2026, 8, 1),
			310000,
		},
		{
			"beyond cycle end caps at full",
			310000, domain.CycleMonthly,
			date(2026, 7, 1), date(2026, 9, 15),
			310000,
		},
		{
			"zero days",
			310000, domain.CycleMonthly,
			date(2026, 7, 1), date(2026, 7, 1),
			0,
		},
		{
			"to before from",
			310000, domain.CycleMonthly,
			date(2026, 7, 10), date(2026, 7, 1),
			0,
		},
		{
			"one day of 31",
			310000, domain.CycleMonthly,
			date(2026, 7, 1), date(2026, 7, 2),
			10000,
		},
		{
			"one_time always full",
			500000, domain.CycleOneTime,
			date(2026, 7, 1), date(2026, 7, 2),
			500000,
		},
		{
			"annual half year",
			1200000, domain.CycleAnnually,
			date(2026, 1, 1), date(2026, 7, 2), // 182 of 365 days
			598356, // 1200000*182/365 = 598356.16 -> 598356
		},
		{
			// Exact .5 tie: 101 * 15/30 = 50.5 -> must round UP to 51
			// (round-half-up, not round-half-to-even/banker's rounding).
			"exact half rounds up",
			101, domain.CycleMonthly,
			date(2026, 6, 1), date(2026, 6, 16), // 15 of 30 days (June)
			51,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.Prorate(tt.amount, tt.cycle, tt.from, tt.to))
		})
	}
}

func TestClientFullName(t *testing.T) {
	assert.Equal(t, "Budi Santoso", domain.Client{FirstName: "Budi", LastName: "Santoso"}.FullName())
	assert.Equal(t, "Budi", domain.Client{FirstName: "Budi"}.FullName())
	assert.Equal(t, "Santoso", domain.Client{LastName: "Santoso"}.FullName())
	assert.Equal(t, "", domain.Client{}.FullName())
}

func TestClientHasRegistrantAddress(t *testing.T) {
	complete := domain.Client{Address1: "Jl. Melati 1", City: "Lamongan", State: "Jawa Timur", Postcode: "62211"}
	assert.True(t, complete.HasRegistrantAddress())

	cases := []domain.Client{
		{City: "Lamongan", State: "Jawa Timur", Postcode: "62211"},
		{Address1: "Jl. Melati 1", State: "Jawa Timur", Postcode: "62211"},
		{Address1: "Jl. Melati 1", City: "Lamongan", Postcode: "62211"},
		{Address1: "Jl. Melati 1", City: "Lamongan", State: "Jawa Timur"},
		{},
	}
	for _, c := range cases {
		assert.False(t, c.HasRegistrantAddress(), "%+v", c)
	}
}
