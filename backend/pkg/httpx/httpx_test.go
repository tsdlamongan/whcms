package httpx_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doRequest(t *testing.T, h fiber.Handler, target string) (int, httpx.Envelope) {
	t.Helper()
	app := fiber.New()
	app.Get("/t", h)
	resp, err := app.Test(httptest.NewRequest("GET", target, nil))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var env httpx.Envelope
	if len(body) > 0 {
		require.NoError(t, json.Unmarshal(body, &env))
	}
	return resp.StatusCode, env
}

func TestOK(t *testing.T) {
	status, env := doRequest(t, func(c fiber.Ctx) error {
		return httpx.OK(c, map[string]string{"hello": "world"})
	}, "/t")
	assert.Equal(t, 200, status)
	assert.Nil(t, env.Error)
	assert.Nil(t, env.Meta)
	assert.Equal(t, map[string]any{"hello": "world"}, env.Data)
}

func TestOKWithMeta(t *testing.T) {
	status, env := doRequest(t, func(c fiber.Ctx) error {
		p := httpx.Page{Page: 2, PerPage: 10}
		return httpx.OK(c, []int{1, 2}, p.Meta(42))
	}, "/t")
	assert.Equal(t, 200, status)
	require.NotNil(t, env.Meta)
	assert.Equal(t, 2, env.Meta.Page)
	assert.Equal(t, 10, env.Meta.PerPage)
	assert.Equal(t, int64(42), env.Meta.Total)
}

func TestCreated(t *testing.T) {
	status, env := doRequest(t, func(c fiber.Ctx) error {
		return httpx.Created(c, map[string]any{"id": 1})
	}, "/t")
	assert.Equal(t, 201, status)
	assert.Nil(t, env.Error)
	assert.NotNil(t, env.Data)
}

func TestNoContent(t *testing.T) {
	status, _ := doRequest(t, httpx.NoContent, "/t")
	assert.Equal(t, 204, status)
}

func TestFailWithAppError(t *testing.T) {
	status, env := doRequest(t, func(c fiber.Ctx) error {
		return httpx.Fail(c, apperr.Validation("invalid input",
			apperr.FieldError{Field: "email", Message: "required"}))
	}, "/t")
	assert.Equal(t, 422, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
	assert.Equal(t, "invalid input", env.Error.Message)
	require.Len(t, env.Error.Details, 1)
	assert.Equal(t, "email", env.Error.Details[0].Field)
	assert.Nil(t, env.Data)
}

func TestFailHidesInternalMessage(t *testing.T) {
	status, env := doRequest(t, func(c fiber.Ctx) error {
		return httpx.Fail(c, errors.New("pq: connection refused secret-dsn"))
	}, "/t")
	assert.Equal(t, 500, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "INTERNAL", env.Error.Code)
	assert.Equal(t, "internal error", env.Error.Message)
}

func TestStatusFor(t *testing.T) {
	tests := []struct {
		code apperr.Code
		want int
	}{
		{apperr.CodeValidation, 422},
		{apperr.CodeUnauthorized, 401},
		{apperr.CodeForbidden, 403},
		{apperr.CodeNotFound, 404},
		{apperr.CodeConflict, 409},
		{apperr.CodeRateLimited, 429},
		{apperr.CodePaymentRequired, 402},
		{apperr.CodeExternal, 502},
		{apperr.CodeInternal, 500},
		{apperr.Code("UNKNOWN"), 500},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, httpx.StatusFor(tt.code), string(tt.code))
	}
}

func TestParsePage(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPage    int
		wantPerPage int
	}{
		{"defaults", "/t", 1, 10},
		{"explicit", "/t?page=3&per_page=50", 3, 50},
		{"large page size", "/t?per_page=1000", 1, 1000},
		{"clamped max", "/t?per_page=5000", 1, 1000},
		{"invalid page", "/t?page=abc&per_page=-1", 1, 10},
		{"zero values", "/t?page=0&per_page=0", 1, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got httpx.Page
			status, _ := doRequest(t, func(c fiber.Ctx) error {
				got = httpx.ParsePage(c)
				return httpx.OK(c, nil)
			}, tt.query)
			assert.Equal(t, 200, status)
			assert.Equal(t, tt.wantPage, got.Page)
			assert.Equal(t, tt.wantPerPage, got.PerPage)
		})
	}
}

func TestPageOffsetLimit(t *testing.T) {
	p := httpx.Page{Page: 3, PerPage: 25}
	assert.Equal(t, 50, p.Offset())
	assert.Equal(t, 25, p.Limit())
}

func TestIdentityHelpers(t *testing.T) {
	app := fiber.New()
	app.Get("/t", func(c fiber.Ctx) error {
		_, ok := httpx.Identity(c)
		assert.False(t, ok)
		assert.Equal(t, int64(0), httpx.MustIdentity(c).UserID)

		httpx.SetIdentity(c, httpx.AuthIdentity{UserID: 7, Role: "client", ClientID: 3, JTI: "j1"})
		id, ok := httpx.Identity(c)
		require.True(t, ok)
		assert.Equal(t, int64(7), id.UserID)
		assert.Equal(t, int64(3), id.ClientID)
		assert.False(t, id.IsAdmin())
		assert.False(t, id.IsStaff())

		assert.True(t, httpx.AuthIdentity{Role: "admin"}.IsAdmin())
		assert.True(t, httpx.AuthIdentity{Role: "admin"}.IsStaff())
		assert.True(t, httpx.AuthIdentity{Role: "staff"}.IsStaff())
		return httpx.OK(c, nil)
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/t", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestQueryID(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    int64
		wantErr bool
	}{
		{"absent means no filter", "/t", 0, false},
		{"blank means no filter", "/t?client_id=", 0, false},
		{"valid id", "/t?client_id=42", 42, false},
		{"zero is rejected", "/t?client_id=0", 0, true},
		{"negative is rejected", "/t?client_id=-3", 0, true},
		{"non numeric is rejected", "/t?client_id=abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				got int64
				err error
			)
			status, env := doRequest(t, func(c fiber.Ctx) error {
				got, err = httpx.QueryID(c, "client_id")
				if err != nil {
					return httpx.Fail(c, err)
				}
				return httpx.OK(c, nil)
			}, tt.query)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, 422, status)
				require.NotNil(t, env.Error)
				assert.Equal(t, "VALIDATION", env.Error.Code)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 200, status)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestQueryDate(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    string // YYYY-MM-DD, "" = zero time
		wantErr bool
	}{
		{"absent means no filter", "/t", "", false},
		{"blank means no filter", "/t?date_from=", "", false},
		{"valid date", "/t?date_from=2026-02-28", "2026-02-28", false},
		{"wrong format is rejected", "/t?date_from=28-02-2026", "", true},
		{"impossible date is rejected", "/t?date_from=2026-02-30", "", true},
		{"garbage is rejected", "/t?date_from=yesterday", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				got time.Time
				err error
			)
			status, env := doRequest(t, func(c fiber.Ctx) error {
				got, err = httpx.QueryDate(c, "date_from")
				if err != nil {
					return httpx.Fail(c, err)
				}
				return httpx.OK(c, nil)
			}, tt.query)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, 422, status)
				require.NotNil(t, env.Error)
				assert.Equal(t, "VALIDATION", env.Error.Code)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 200, status)
			if tt.want == "" {
				assert.True(t, got.IsZero())
				return
			}
			assert.Equal(t, tt.want, got.Format(httpx.DateLayout))
		})
	}
}

func TestQueryIDs(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    []int64
		wantErr bool
	}{
		{"absent means no filter", "/t", nil, false},
		{"blank means no filter", "/t?ids=", nil, false},
		{"single id", "/t?ids=7", []int64{7}, false},
		{"several ids with blanks tolerated", "/t?ids=3,%204,5%20", []int64{3, 4, 5}, false},
		{"zero is rejected", "/t?ids=1,0", nil, true},
		{"negative is rejected", "/t?ids=-1", nil, true},
		{"non numeric is rejected", "/t?ids=1,x", nil, true},
		{"empty item is rejected", "/t?ids=1,,2", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				got []int64
				err error
			)
			status, env := doRequest(t, func(c fiber.Ctx) error {
				got, err = httpx.QueryIDs(c, "ids")
				if err != nil {
					return httpx.Fail(c, err)
				}
				return httpx.OK(c, nil)
			}, tt.query)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, 422, status)
				require.NotNil(t, env.Error)
				assert.Equal(t, "VALIDATION", env.Error.Code)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 200, status)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("more than MaxPerPage ids is rejected", func(t *testing.T) {
		ids := strings.TrimSuffix(strings.Repeat("1,", httpx.MaxPerPage+1), ",")
		status, _ := doRequest(t, func(c fiber.Ctx) error {
			_, err := httpx.QueryIDs(c, "ids")
			if err != nil {
				return httpx.Fail(c, err)
			}
			return httpx.OK(c, nil)
		}, "/t?ids="+ids)
		assert.Equal(t, 422, status)
	})

	t.Run("exactly MaxPerPage ids is accepted", func(t *testing.T) {
		ids := strings.TrimSuffix(strings.Repeat("1,", httpx.MaxPerPage), ",")
		var got []int64
		status, _ := doRequest(t, func(c fiber.Ctx) error {
			var err error
			got, err = httpx.QueryIDs(c, "ids")
			if err != nil {
				return httpx.Fail(c, err)
			}
			return httpx.OK(c, nil)
		}, "/t?ids="+ids)
		assert.Equal(t, 200, status)
		assert.Len(t, got, httpx.MaxPerPage)
	})
}
