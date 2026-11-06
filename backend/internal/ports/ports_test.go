package ports_test

import (
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListParamsLimit(t *testing.T) {
	tests := []struct {
		name    string
		perPage int
		want    int
	}{
		{"zero defaults to 25", 0, 25},
		{"negative defaults to 25", -10, 25},
		{"within range passes through", 10, 10},
		{"exactly 1 passes through", 1, 1},
		{"exactly 100 passes through", 100, 100},
		{"the page-size selector's 500 passes through", 500, 500},
		{"exactly the cap passes through", 1000, 1000},
		{"above the cap clamps to 1000", 1500, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ports.ListParams{PerPage: tt.perPage}
			assert.Equal(t, tt.want, p.Limit())
		})
	}
}

func TestListParamsOffset(t *testing.T) {
	tests := []struct {
		name    string
		page    int
		perPage int
		want    int
	}{
		{"page zero defaults to page 1 -> offset 0", 0, 25, 0},
		{"negative page defaults to page 1 -> offset 0", -3, 25, 0},
		{"page 1 -> offset 0 regardless of perPage", 1, 10, 0},
		{"page 3 with perPage 10 -> offset 20", 3, 10, 20},
		{"page 2 with default perPage (25) -> offset 25", 2, 0, 25},
		{"page 2 with a 500 page size -> offset 500", 2, 500, 500},
		{"page 2 with perPage clamped to 1000 -> offset 1000", 2, 1500, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ports.ListParams{Page: tt.page, PerPage: tt.perPage}
			assert.Equal(t, tt.want, p.Offset())
		})
	}
}

func TestListParamsOffsetDoesNotMutateReceiver(t *testing.T) {
	// Offset/Limit take ListParams by value, so calling them must never
	// affect the caller's copy of Page/PerPage.
	p := ports.ListParams{Page: 0, PerPage: 0}
	_ = p.Offset()
	assert.Equal(t, 0, p.Page)
	assert.Equal(t, 0, p.PerPage)
}

func TestJobOptions(t *testing.T) {
	t.Run("WithQueue sets Queue", func(t *testing.T) {
		o := &ports.JobOptions{}
		ports.WithQueue("emails")(o)
		assert.Equal(t, "emails", o.Queue)
	})

	t.Run("WithMaxRetry sets MaxRetry", func(t *testing.T) {
		o := &ports.JobOptions{}
		ports.WithMaxRetry(7)(o)
		assert.Equal(t, 7, o.MaxRetry)
	})

	t.Run("WithProcessIn sets ProcessIn", func(t *testing.T) {
		o := &ports.JobOptions{}
		ports.WithProcessIn(5 * time.Minute)(o)
		assert.Equal(t, 5*time.Minute, o.ProcessIn)
	})

	t.Run("WithUniqueTTL sets UniqueTTL", func(t *testing.T) {
		o := &ports.JobOptions{}
		ports.WithUniqueTTL(30 * time.Second)(o)
		assert.Equal(t, 30*time.Second, o.UniqueTTL)
	})

	t.Run("composed options apply independently onto the same struct", func(t *testing.T) {
		o := &ports.JobOptions{}
		opts := []ports.JobOption{
			ports.WithQueue("renewals"),
			ports.WithMaxRetry(3),
			ports.WithProcessIn(time.Hour),
			ports.WithUniqueTTL(2 * time.Hour),
		}
		for _, apply := range opts {
			apply(o)
		}
		assert.Equal(t, ports.JobOptions{
			Queue:     "renewals",
			MaxRetry:  3,
			ProcessIn: time.Hour,
			UniqueTTL: 2 * time.Hour,
		}, *o)
	})
}

func TestListParamsCreatedBounds(t *testing.T) {
	day := func(s string) time.Time {
		v, err := time.Parse("2006-01-02", s)
		require.NoError(t, err)
		return v
	}

	t.Run("unset bounds are nil (unbounded)", func(t *testing.T) {
		from, to := ports.ListParams{}.CreatedBounds()
		assert.Nil(t, from)
		assert.Nil(t, to)
	})

	t.Run("from is the start of its UTC day, to is exclusive of the day after", func(t *testing.T) {
		from, to := ports.ListParams{From: day("2026-02-28"), To: day("2026-02-28")}.CreatedBounds()
		require.NotNil(t, from)
		require.NotNil(t, to)
		assert.Equal(t, "2026-02-28T00:00:00Z", from.Format(time.RFC3339))
		assert.Equal(t, "2026-03-01T00:00:00Z", to.Format(time.RFC3339), "To is inclusive: the whole day counts")
	})

	t.Run("only one side set leaves the other open", func(t *testing.T) {
		from, to := ports.ListParams{From: day("2026-01-01")}.CreatedBounds()
		require.NotNil(t, from)
		assert.Nil(t, to)

		from, to = ports.ListParams{To: day("2026-12-31")}.CreatedBounds()
		assert.Nil(t, from)
		require.NotNil(t, to)
		assert.Equal(t, "2027-01-01T00:00:00Z", to.Format(time.RFC3339))
	})

	t.Run("a non-UTC instant is normalised to its UTC day", func(t *testing.T) {
		wib := time.FixedZone("WIB", 7*3600)
		// 01:00 on 2 Jan in Jakarta is still 1 Jan in UTC.
		from, _ := ports.ListParams{From: time.Date(2026, 1, 2, 1, 0, 0, 0, wib)}.CreatedBounds()
		require.NotNil(t, from)
		assert.Equal(t, "2026-01-01T00:00:00Z", from.Format(time.RFC3339))
	})
}
