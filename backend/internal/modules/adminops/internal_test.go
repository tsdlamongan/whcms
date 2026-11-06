package adminops

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPageLimitAndOffset(t *testing.T) {
	tests := []struct {
		name    string
		perPage int
		want    int
	}{
		{"zero defaults to 25", 0, 25},
		{"negative defaults to 25", -4, 25},
		{"within range passes through", 40, 40},
		{"the page-size selector's 500 passes through", 500, 500},
		{"exactly the cap passes through", 1000, 1000},
		{"above the cap clamps to 1000", 4000, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, pageLimit(tt.perPage))
		})
	}

	assert.Equal(t, 0, pageOffset(0, 10), "page below 1 is page 1")
	assert.Equal(t, 20, pageOffset(3, 10))
	assert.Equal(t, 500, pageOffset(2, 500))
	assert.Equal(t, 1000, pageOffset(2, 9999), "offset uses the clamped limit")
}
