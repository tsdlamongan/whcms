package clients

// White-box tests for small unexported helpers that have no reachable
// black-box path through the package's public API (SearchInput.limit/offset
// are only invoked from Repo query builders with real pagination values,
// applyProfilePatch's unknown-field guard and normalize's nil guard are
// defensive branches every current call site already avoids). Kept minimal
// and separate from the package's public (clients_test) test files.

import (
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestSearchInputLimit(t *testing.T) {
	tests := []struct {
		name    string
		perPage int
		want    int
	}{
		{"below minimum defaults to 25", 0, 25},
		{"negative defaults to 25", -5, 25},
		{"within range passes through", 40, 40},
		{"the page-size selector's 500 passes through", 500, 500},
		{"exactly the cap passes through", 1000, 1000},
		{"above cap clamps to 1000", 5000, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := SearchInput{PerPage: tt.perPage}
			assert.Equal(t, tt.want, f.limit())
		})
	}
}

func TestSearchInputOffset(t *testing.T) {
	tests := []struct {
		name    string
		page    int
		perPage int
		want    int
	}{
		{"page below 1 treated as page 1", 0, 25, 0},
		{"page 2 with default per page", 2, 0, 25},
		{"page 3 with explicit per page", 3, 10, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := SearchInput{Page: tt.page, PerPage: tt.perPage}
			assert.Equal(t, tt.want, f.offset())
		})
	}
}

func TestApplyProfilePatchUnknownFieldIgnored(t *testing.T) {
	c := &domain.Client{FirstName: "Budi"}
	v := "ignored"
	before, after := applyProfilePatch(c, map[string]*string{"not_a_real_field": &v})
	assert.Empty(t, before)
	assert.Empty(t, after)
	assert.Equal(t, "Budi", c.FirstName)
}

func TestNormalizeNilIsNil(t *testing.T) {
	assert.NoError(t, normalize(nil))
}
