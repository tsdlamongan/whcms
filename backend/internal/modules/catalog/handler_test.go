package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/catalog"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fake middleware bundle

// fakeMW stubs catalog.Middlewares: injects a fixed identity and mirrors the
// production role/permission semantics. rateLimited counts RateLimit hits.
type fakeMW struct {
	identity    httpx.AuthIdentity
	authFail    bool
	permissions map[string]bool
	rateLimited int
}

func (m *fakeMW) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		if m.authFail {
			return apperr.Unauthorized("missing bearer token")
		}
		httpx.SetIdentity(c, m.identity)
		return c.Next()
	}
}

func (m *fakeMW) RequireRole(roles ...string) fiber.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if !allowed[id.Role] {
			return apperr.Forbidden("insufficient role")
		}
		return c.Next()
	}
}

func (m *fakeMW) RequirePermission(module string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if id.Role == "admin" {
			return c.Next()
		}
		if !m.permissions[module] {
			return apperr.Forbidden("missing permission: " + module)
		}
		return c.Next()
	}
}

func (m *fakeMW) RateLimit(prefix string, limit int, window time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		m.rateLimited++
		return c.Next()
	}
}

var _ catalog.Middlewares = (*fakeMW)(nil)

// Harness

type harness struct {
	app *fiber.App
	fx  *fixtures
	mw  *fakeMW
}

// newApp builds a Fiber app with the real *Service over the shared fakes
// (see service_test.go) and the fake middleware bundle.
func newApp(mut func(*fakeMW)) *harness {
	fx := newFixture()
	mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 1, Role: "admin"}}
	if mut != nil {
		mut(mw)
	}
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error { return httpx.Fail(c, err) },
	})
	_ = slog.New(slog.NewTextHandler(io.Discard, nil)) // parity with prod wiring
	h := catalog.NewHandler(fx.svc, mw)
	h.RegisterRoutes(app.Group("/api/v1"))
	return &harness{app: app, fx: fx, mw: mw}
}

func (h *harness) do(t *testing.T, method, path string, body any) (*http.Response, httpx.Envelope) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.app.Test(req)
	require.NoError(t, err)
	var env httpx.Envelope
	if resp.StatusCode != fiber.StatusNoContent {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	}
	_ = resp.Body.Close()
	return resp, env
}

// Public routes

func TestHandlerPublicCatalog(t *testing.T) {
	h := newApp(nil)
	visibleFixture(h.fx)

	resp, env := h.do(t, "GET", "/api/v1/products", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Nil(t, env.Error)
	assert.Equal(t, 1, h.mw.rateLimited, "public route is rate limited")
}

func TestHandlerPublicGroups(t *testing.T) {
	h := newApp(nil)
	h.fx.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
		return []domain.ProductGroup{{ID: 1, Name: "Hosting"}}, nil
	}
	resp, env := h.do(t, "GET", "/api/v1/product-groups", nil)
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerPublicProductNotFound(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return nil, apperr.NotFound("product")
	}
	resp, env := h.do(t, "GET", "/api/v1/products/nope", nil)
	assert.Equal(t, 404, resp.StatusCode)
	require.NotNil(t, env.Error)
	assert.Equal(t, "NOT_FOUND", env.Error.Code)
}

func TestHandlerValidateCoupon(t *testing.T) {
	h := newApp(nil)
	h.fx.coupons.GetByCodeFn = func(ctx context.Context, code string) (*domain.Coupon, error) {
		return coupon(nil), nil
	}
	resp, env := h.do(t, "POST", "/api/v1/coupons/validate", map[string]any{
		"code": "save10", "subtotal": 100000,
	})
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
	raw, _ := json.Marshal(env.Data)
	var res catalog.ValidateCouponResult
	require.NoError(t, json.Unmarshal(raw, &res))
	assert.Equal(t, "SAVE10", res.Code)
	assert.Equal(t, int64(10_000), res.Discount)
}

func TestHandlerValidateCouponExpired(t *testing.T) {
	h := newApp(nil)
	h.fx.coupons.GetByCodeFn = func(ctx context.Context, code string) (*domain.Coupon, error) {
		return coupon(func(c *domain.Coupon) {
			c.ExpiresAt = ptr(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		}), nil
	}
	resp, env := h.do(t, "POST", "/api/v1/coupons/validate", map[string]any{
		"code": "SAVE10", "subtotal": 100000,
	})
	assert.Equal(t, 422, resp.StatusCode)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

// Admin RBAC

func TestHandlerAdminRequiresAuth(t *testing.T) {
	h := newApp(func(m *fakeMW) { m.authFail = true })
	resp, env := h.do(t, "GET", "/api/v1/admin/products", nil)
	assert.Equal(t, 401, resp.StatusCode)
	assert.Equal(t, "UNAUTHORIZED", env.Error.Code)
}

func TestHandlerAdminRejectsClientRole(t *testing.T) {
	h := newApp(func(m *fakeMW) {
		m.identity = httpx.AuthIdentity{UserID: 5, Role: "client", ClientID: 2}
	})
	resp, env := h.do(t, "GET", "/api/v1/admin/products", nil)
	assert.Equal(t, 403, resp.StatusCode)
	assert.Equal(t, "FORBIDDEN", env.Error.Code)
}

func TestHandlerAdminStaffNeedsProductsPermission(t *testing.T) {
	h := newApp(func(m *fakeMW) {
		m.identity = httpx.AuthIdentity{UserID: 5, Role: "staff"}
		m.permissions = map[string]bool{"billing": true}
	})
	resp, _ := h.do(t, "GET", "/api/v1/admin/products", nil)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestHandlerAdminStaffWithPermission(t *testing.T) {
	h := newApp(func(m *fakeMW) {
		m.identity = httpx.AuthIdentity{UserID: 5, Role: "staff"}
		m.permissions = map[string]bool{"products": true}
	})
	resp, _ := h.do(t, "GET", "/api/v1/admin/products", nil)
	assert.Equal(t, 200, resp.StatusCode)
}

// Admin CRUD flows

func TestHandlerCreateGroup(t *testing.T) {
	h := newApp(nil)
	h.fx.products.CreateGroupFn = func(ctx context.Context, g *domain.ProductGroup) error {
		g.ID = 3
		return nil
	}
	resp, env := h.do(t, "POST", "/api/v1/admin/product-groups", map[string]any{"name": "Hosting"})
	assert.Equal(t, 201, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerCreateGroupInvalidBody(t *testing.T) {
	h := newApp(nil)
	resp, env := h.do(t, "POST", "/api/v1/admin/product-groups", map[string]any{"name": "x"})
	assert.Equal(t, 422, resp.StatusCode)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerDeleteGroupConflict(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	h.fx.products.CountProductsInGroupFn = func(ctx context.Context, groupID int64) (int64, error) {
		return 2, nil
	}
	resp, env := h.do(t, "DELETE", "/api/v1/admin/product-groups/4", nil)
	assert.Equal(t, 409, resp.StatusCode)
	assert.Equal(t, "CONFLICT", env.Error.Code)
}

func TestHandlerDeleteGroupNoContent(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	resp, _ := h.do(t, "DELETE", "/api/v1/admin/product-groups/4", nil)
	assert.Equal(t, 204, resp.StatusCode)
}

func TestHandlerInvalidID(t *testing.T) {
	h := newApp(nil)
	resp, env := h.do(t, "GET", "/api/v1/admin/products/abc", nil)
	assert.Equal(t, 422, resp.StatusCode)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerCreateProduct(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	h.fx.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 10
		return nil
	}
	resp, env := h.do(t, "POST", "/api/v1/admin/products", map[string]any{
		"group_id": 1, "name": "Basic Hosting", "type": "shared_hosting",
	})
	assert.Equal(t, 201, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerUpdateProduct(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductOther, Module: domain.ModuleNone, AutoSetup: domain.SetupOnPayment}, nil
	}
	resp, env := h.do(t, "PATCH", "/api/v1/admin/products/10", map[string]any{"hidden": true})
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerDuplicateProduct(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductOther, Module: domain.ModuleNone, AutoSetup: domain.SetupOnPayment}, nil
	}
	h.fx.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 20
		return nil
	}
	resp, env := h.do(t, "POST", "/api/v1/admin/products/10/duplicate", nil)
	assert.Equal(t, 201, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerUpsertPricingNonIDR(t *testing.T) {
	h := newApp(nil)
	resp, env := h.do(t, "PUT", "/api/v1/admin/products/10/pricing", map[string]any{
		"cycle": "monthly", "price": 50000, "currency": "USD",
	})
	assert.Equal(t, 422, resp.StatusCode)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestHandlerPricingLifecycle(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id}, nil
	}
	resp, env := h.do(t, "PUT", "/api/v1/admin/products/10/pricing", map[string]any{
		"cycle": "monthly", "price": 50000,
	})
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)

	resp, env = h.do(t, "GET", "/api/v1/admin/products/10/pricing", nil)
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)

	resp, _ = h.do(t, "DELETE", "/api/v1/admin/products/10/pricing/monthly", nil)
	assert.Equal(t, 204, resp.StatusCode)
}

func TestHandlerOptionEndpoints(t *testing.T) {
	h := newApp(nil)
	h.fx.options.CreateOptionGroupFn = func(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
		g.ID = 1
		return nil
	}
	resp, _ := h.do(t, "POST", "/api/v1/admin/config-options/groups", map[string]any{"name": "Extras"})
	assert.Equal(t, 201, resp.StatusCode)

	resp, _ = h.do(t, "POST", "/api/v1/admin/config-options/groups/1/options", map[string]any{"name": "RAM"})
	assert.Equal(t, 201, resp.StatusCode)

	resp, _ = h.do(t, "POST", "/api/v1/admin/config-options/options/2/values", map[string]any{
		"name": "2GB", "price_deltas": map[string]int64{"monthly": 10000},
	})
	assert.Equal(t, 201, resp.StatusCode)

	resp, _ = h.do(t, "PATCH", "/api/v1/admin/config-options/values/3", map[string]any{"name": "4GB"})
	assert.Equal(t, 200, resp.StatusCode)

	resp, _ = h.do(t, "PATCH", "/api/v1/admin/config-options/options/2", map[string]any{"name": "CPU"})
	assert.Equal(t, 200, resp.StatusCode)

	resp, _ = h.do(t, "PATCH", "/api/v1/admin/config-options/groups/1", map[string]any{"name": "More"})
	assert.Equal(t, 200, resp.StatusCode)

	resp, _ = h.do(t, "GET", "/api/v1/admin/config-options", nil)
	assert.Equal(t, 200, resp.StatusCode)

	resp, _ = h.do(t, "DELETE", "/api/v1/admin/config-options/values/3", nil)
	assert.Equal(t, 204, resp.StatusCode)
	resp, _ = h.do(t, "DELETE", "/api/v1/admin/config-options/options/2", nil)
	assert.Equal(t, 204, resp.StatusCode)
	resp, _ = h.do(t, "DELETE", "/api/v1/admin/config-options/groups/1", nil)
	assert.Equal(t, 204, resp.StatusCode)
}

func TestHandlerCouponLifecycle(t *testing.T) {
	h := newApp(nil)
	h.fx.coupons.CreateFn = func(ctx context.Context, c *domain.Coupon) error {
		c.ID = 7
		return nil
	}
	resp, env := h.do(t, "POST", "/api/v1/admin/coupons", map[string]any{
		"code": "save10", "type": "percentage", "value": 10,
	})
	assert.Equal(t, 201, resp.StatusCode)
	require.Nil(t, env.Error)

	h.fx.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) {
		return &domain.Coupon{ID: id, Code: "SAVE10", Type: domain.CouponPercentage, Value: 10, Active: true}, nil
	}
	resp, _ = h.do(t, "GET", "/api/v1/admin/coupons/7", nil)
	assert.Equal(t, 200, resp.StatusCode)

	resp, _ = h.do(t, "PATCH", "/api/v1/admin/coupons/7", map[string]any{"active": false})
	assert.Equal(t, 200, resp.StatusCode)

	resp, _ = h.do(t, "DELETE", "/api/v1/admin/coupons/7", nil)
	assert.Equal(t, 204, resp.StatusCode)

	resp, env = h.do(t, "GET", "/api/v1/admin/coupons", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.NotNil(t, env.Meta, "list responses are paginated")
}

func TestHandlerListProductsMeta(t *testing.T) {
	h := newApp(nil)
	h.fx.products.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
		assert.Equal(t, "basic", p.Search)
		assert.Equal(t, "visible", p.Status)
		return []domain.Product{{ID: 1}}, 1, nil
	}
	resp, env := h.do(t, "GET", "/api/v1/admin/products?search=basic&status=visible&page=2&per_page=10", nil)
	assert.Equal(t, 200, resp.StatusCode)
	require.NotNil(t, env.Meta)
	assert.Equal(t, 2, env.Meta.Page)
	assert.Equal(t, int64(1), env.Meta.Total)
}

func TestHandlerListProductsGroupAndTypeFilters(t *testing.T) {
	h := newApp(nil)
	var got ports.ListParams
	called := false
	h.fx.products.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
		called, got = true, p
		return nil, 0, nil
	}
	resp, _ := h.do(t, "GET", "/api/v1/admin/products?group_id=4&type=shared_hosting", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(4), got.GroupID)
	assert.Equal(t, "shared_hosting", got.Type)

	called = false
	resp, _ = h.do(t, "GET", "/api/v1/admin/products?group_id=oops", nil)
	assert.Equal(t, 422, resp.StatusCode)
	assert.False(t, called)
}

func TestHandlerAdminGroupsListing(t *testing.T) {
	h := newApp(nil)
	var gotInclude bool
	h.fx.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
		gotInclude = includeHidden
		return nil, nil
	}
	resp, _ := h.do(t, "GET", "/api/v1/admin/product-groups", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, gotInclude, "admin listing includes hidden by default")

	resp, _ = h.do(t, "GET", "/api/v1/admin/product-groups?include_hidden=false", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.False(t, gotInclude)
}

// Single-resource endpoints: a stored group/product is returned or updated
// successfully, and a missing one yields 404 NOT_FOUND.

func TestHandlerAdminGetGroup(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id, Name: "Hosting"}, nil
	}
	resp, env := h.do(t, "GET", "/api/v1/admin/product-groups/4", nil)
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerAdminGetGroupNotFound(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return nil, apperr.NotFound("product group")
	}
	resp, env := h.do(t, "GET", "/api/v1/admin/product-groups/4", nil)
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "NOT_FOUND", env.Error.Code)
}

func TestHandlerAdminUpdateGroup(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id, Name: "Old", Slug: "old"}, nil
	}
	resp, env := h.do(t, "PATCH", "/api/v1/admin/product-groups/4", map[string]any{"name": "New Name"})
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerAdminUpdateGroupNotFound(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return nil, apperr.NotFound("product group")
	}
	resp, env := h.do(t, "PATCH", "/api/v1/admin/product-groups/4", map[string]any{"name": "New Name"})
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "NOT_FOUND", env.Error.Code)
}

func TestHandlerAdminGetProduct(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, Name: "Basic"}, nil
	}
	resp, env := h.do(t, "GET", "/api/v1/admin/products/10", nil)
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
}

func TestHandlerAdminDeleteProduct(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id}, nil
	}
	resp, _ := h.do(t, "DELETE", "/api/v1/admin/products/10", nil)
	assert.Equal(t, 204, resp.StatusCode)
}

func TestHandlerAdminDeleteProductNotFound(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return nil, apperr.NotFound("product")
	}
	resp, env := h.do(t, "DELETE", "/api/v1/admin/products/10", nil)
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "NOT_FOUND", env.Error.Code)
}

func TestHandlerPublicProductSuccess(t *testing.T) {
	h := newApp(nil)
	h.fx.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return &domain.Product{ID: 1, GroupID: 1, Slug: slug, Name: "Basic"}, nil
	}
	h.fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	resp, env := h.do(t, "GET", "/api/v1/products/basic", nil)
	assert.Equal(t, 200, resp.StatusCode)
	require.Nil(t, env.Error)
}

// Malformed JSON body: every Bind().Body endpoint must reject non-JSON with
// 422 VALIDATION, independent of whatever the service layer would say.

func TestHandlerAdminInvalidJSONBodySweep(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"validate coupon", "POST", "/api/v1/coupons/validate"},
		{"create group", "POST", "/api/v1/admin/product-groups"},
		{"update group", "PATCH", "/api/v1/admin/product-groups/1"},
		{"create product", "POST", "/api/v1/admin/products"},
		{"update product", "PATCH", "/api/v1/admin/products/1"},
		{"upsert pricing", "PUT", "/api/v1/admin/products/1/pricing"},
		{"create option group", "POST", "/api/v1/admin/config-options/groups"},
		{"update option group", "PATCH", "/api/v1/admin/config-options/groups/1"},
		{"create option", "POST", "/api/v1/admin/config-options/groups/1/options"},
		{"update option", "PATCH", "/api/v1/admin/config-options/options/1"},
		{"create option value", "POST", "/api/v1/admin/config-options/options/1/values"},
		{"update option value", "PATCH", "/api/v1/admin/config-options/values/1"},
		{"create coupon", "POST", "/api/v1/admin/coupons"},
		{"update coupon", "PATCH", "/api/v1/admin/coupons/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newApp(nil)
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader([]byte(`{"broken`)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := h.app.Test(req)
			require.NoError(t, err)
			assert.Equal(t, 422, resp.StatusCode)
			_ = resp.Body.Close()
		})
	}
}

// Invalid :id / :cycle params: every parseID-guarded endpoint must reject a
// non-numeric id with 422 VALIDATION before ever reaching the service.

func TestHandlerAdminInvalidIDSweep(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"get group", "GET", "/api/v1/admin/product-groups/abc"},
		{"update group", "PATCH", "/api/v1/admin/product-groups/abc"},
		{"delete group", "DELETE", "/api/v1/admin/product-groups/abc"},
		{"update product", "PATCH", "/api/v1/admin/products/abc"},
		{"delete product", "DELETE", "/api/v1/admin/products/abc"},
		{"list pricing", "GET", "/api/v1/admin/products/abc/pricing"},
		{"upsert pricing", "PUT", "/api/v1/admin/products/abc/pricing"},
		{"delete pricing", "DELETE", "/api/v1/admin/products/abc/pricing/monthly"},
		{"update option group", "PATCH", "/api/v1/admin/config-options/groups/abc"},
		{"delete option group", "DELETE", "/api/v1/admin/config-options/groups/abc"},
		{"create option", "POST", "/api/v1/admin/config-options/groups/abc/options"},
		{"update option", "PATCH", "/api/v1/admin/config-options/options/abc"},
		{"delete option", "DELETE", "/api/v1/admin/config-options/options/abc"},
		{"create option value", "POST", "/api/v1/admin/config-options/options/abc/values"},
		{"update option value", "PATCH", "/api/v1/admin/config-options/values/abc"},
		{"delete option value", "DELETE", "/api/v1/admin/config-options/values/abc"},
		{"get coupon", "GET", "/api/v1/admin/coupons/abc"},
		{"update coupon", "PATCH", "/api/v1/admin/coupons/abc"},
		{"delete coupon", "DELETE", "/api/v1/admin/coupons/abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newApp(nil)
			resp, env := h.do(t, tt.method, tt.path, nil)
			assert.Equal(t, 422, resp.StatusCode)
			require.NotNil(t, env.Error)
			assert.Equal(t, "VALIDATION", env.Error.Code)
		})
	}
}

// Service-layer error propagation: once the body binds and the id parses,
// a failing repo must surface through the handler as an error envelope,
// never as a 2xx.

func TestHandlerAdminServiceErrorSweep(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   any
		prep   func(fx *fixtures)
	}{
		{"public catalog", "GET", "/api/v1/products", nil, func(fx *fixtures) {
			fx.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
				return nil, errBoom
			}
		}},
		{"public groups", "GET", "/api/v1/product-groups", nil, func(fx *fixtures) {
			fx.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
				return nil, errBoom
			}
		}},
		{"admin list groups", "GET", "/api/v1/admin/product-groups", nil, func(fx *fixtures) {
			fx.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
				return nil, errBoom
			}
		}},
		{"admin list products", "GET", "/api/v1/admin/products", nil, func(fx *fixtures) {
			fx.products.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
				return nil, 0, errBoom
			}
		}},
		{"admin create product", "POST", "/api/v1/admin/products",
			map[string]any{"group_id": 1, "name": "Basic Hosting", "type": "shared_hosting"},
			func(fx *fixtures) {
				fx.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
					return &domain.ProductGroup{ID: id}, nil
				}
				fx.products.CreateFn = func(ctx context.Context, pr *domain.Product) error { return errBoom }
			}},
		{"admin get product", "GET", "/api/v1/admin/products/10", nil, func(fx *fixtures) {
			fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) { return nil, errBoom }
		}},
		{"admin update product", "PATCH", "/api/v1/admin/products/10", map[string]any{"hidden": true}, func(fx *fixtures) {
			fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) { return nil, errBoom }
		}},
		{"admin list pricing", "GET", "/api/v1/admin/products/10/pricing", nil, func(fx *fixtures) {
			fx.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) { return nil, errBoom }
		}},
		{"admin delete pricing", "DELETE", "/api/v1/admin/products/10/pricing/monthly", nil, func(fx *fixtures) {
			fx.products.DeletePricingFn = func(ctx context.Context, productID int64, cycle domain.BillingCycle) error {
				return errBoom
			}
		}},
		{"admin option tree", "GET", "/api/v1/admin/config-options", nil, func(fx *fixtures) {
			fx.products.ListOptionGroupsFn = func(ctx context.Context) ([]domain.ConfigurableOptionGroup, error) {
				return nil, errBoom
			}
		}},
		{"admin create option group", "POST", "/api/v1/admin/config-options/groups",
			map[string]any{"name": "Extras"}, func(fx *fixtures) {
				fx.options.CreateOptionGroupFn = func(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
					return errBoom
				}
			}},
		{"admin update option group", "PATCH", "/api/v1/admin/config-options/groups/1",
			map[string]any{"name": "Extras2"}, func(fx *fixtures) {
				fx.options.GetOptionGroupByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error) {
					return nil, errBoom
				}
			}},
		{"admin delete option group", "DELETE", "/api/v1/admin/config-options/groups/1", nil, func(fx *fixtures) {
			fx.options.DeleteOptionGroupFn = func(ctx context.Context, id int64) error { return errBoom }
		}},
		{"admin create option", "POST", "/api/v1/admin/config-options/groups/1/options",
			map[string]any{"name": "RAM"}, func(fx *fixtures) {
				fx.options.GetOptionGroupByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error) {
					return nil, errBoom
				}
			}},
		{"admin update option", "PATCH", "/api/v1/admin/config-options/options/2",
			map[string]any{"name": "CPU"}, func(fx *fixtures) {
				fx.options.GetOptionByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOption, error) {
					return nil, errBoom
				}
			}},
		{"admin delete option", "DELETE", "/api/v1/admin/config-options/options/2", nil, func(fx *fixtures) {
			fx.options.DeleteOptionFn = func(ctx context.Context, id int64) error { return errBoom }
		}},
		{"admin create option value", "POST", "/api/v1/admin/config-options/options/2/values",
			map[string]any{"name": "2GB"}, func(fx *fixtures) {
				fx.options.GetOptionByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOption, error) {
					return nil, errBoom
				}
			}},
		{"admin update option value", "PATCH", "/api/v1/admin/config-options/values/3",
			map[string]any{"name": "4GB"}, func(fx *fixtures) {
				fx.options.GetOptionValueByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOptionValue, error) {
					return nil, errBoom
				}
			}},
		{"admin delete option value", "DELETE", "/api/v1/admin/config-options/values/3", nil, func(fx *fixtures) {
			fx.options.DeleteOptionValueFn = func(ctx context.Context, id int64) error { return errBoom }
		}},
		{"admin list coupons", "GET", "/api/v1/admin/coupons", nil, func(fx *fixtures) {
			fx.coupons.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error) {
				return nil, 0, errBoom
			}
		}},
		{"admin create coupon", "POST", "/api/v1/admin/coupons",
			map[string]any{"code": "SAVE10", "type": "percentage", "value": 10}, func(fx *fixtures) {
				fx.coupons.CreateFn = func(ctx context.Context, c *domain.Coupon) error { return errBoom }
			}},
		{"admin get coupon", "GET", "/api/v1/admin/coupons/7", nil, func(fx *fixtures) {
			fx.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) { return nil, errBoom }
		}},
		{"admin update coupon", "PATCH", "/api/v1/admin/coupons/7", map[string]any{"active": false}, func(fx *fixtures) {
			fx.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) { return nil, errBoom }
		}},
		{"admin delete coupon", "DELETE", "/api/v1/admin/coupons/7", nil, func(fx *fixtures) {
			fx.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) { return nil, errBoom }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newApp(nil)
			tt.prep(h.fx)
			resp, env := h.do(t, tt.method, tt.path, tt.body)
			assert.NotEqual(t, fiber.StatusOK, resp.StatusCode)
			assert.NotEqual(t, fiber.StatusCreated, resp.StatusCode)
			assert.NotEqual(t, fiber.StatusNoContent, resp.StatusCode)
			require.NotNil(t, env.Error)
		})
	}
}
