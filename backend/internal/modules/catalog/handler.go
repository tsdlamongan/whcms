package catalog

import (
	"context"
	"strconv"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
)

// Middlewares is the narrow middleware surface the handler needs; satisfied
// by *transporthttp.Middleware.
type Middlewares interface {
	RequireAuth() fiber.Handler
	RequireRole(roles ...string) fiber.Handler
	RequirePermission(module string) fiber.Handler
	RateLimit(prefix string, limit int, window time.Duration) fiber.Handler
}

var _ Middlewares = (*transporthttp.Middleware)(nil)

// CatalogService is the use-case surface consumed by the handler
// (implemented by *Service).
type CatalogService interface {
	// Public
	PublicCatalog(ctx context.Context) ([]PublicGroup, error)
	PublicGroups(ctx context.Context) ([]domain.ProductGroup, error)
	PublicProductBySlug(ctx context.Context, slug string) (*PublicProductDetail, error)
	ValidateCoupon(ctx context.Context, code string, productIDs []int64, subtotal int64) (int64, *domain.Coupon, error)
	// Groups
	ListGroups(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error)
	GetGroup(ctx context.Context, id int64) (*domain.ProductGroup, error)
	CreateGroup(ctx context.Context, actorUserID int64, in GroupInput) (*domain.ProductGroup, error)
	UpdateGroup(ctx context.Context, actorUserID, id int64, in GroupUpdateInput) (*domain.ProductGroup, error)
	DeleteGroup(ctx context.Context, actorUserID, id int64) error
	// Products
	ListProducts(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error)
	GetProduct(ctx context.Context, id int64) (*domain.Product, error)
	CreateProduct(ctx context.Context, actorUserID int64, in ProductInput) (*domain.Product, error)
	UpdateProduct(ctx context.Context, actorUserID, id int64, in ProductUpdateInput) (*domain.Product, error)
	DeleteProduct(ctx context.Context, actorUserID, id int64) error
	DuplicateProduct(ctx context.Context, actorUserID, id int64) (*domain.Product, error)
	// Pricing
	ListPricing(ctx context.Context, productID int64) ([]domain.ProductPricing, error)
	UpsertPricing(ctx context.Context, actorUserID, productID int64, in PricingInput) (*domain.ProductPricing, error)
	DeletePricing(ctx context.Context, actorUserID, productID int64, cycle string) error
	// Dynamic-product specs
	ListProductSpecs(ctx context.Context, productID int64) ([]SpecWithPricing, error)
	CreateSpec(ctx context.Context, actorUserID, productID int64, in SpecInput) (*domain.ProductSpec, error)
	UpdateSpec(ctx context.Context, actorUserID, specID int64, in SpecInput) (*domain.ProductSpec, error)
	DeleteSpec(ctx context.Context, actorUserID, specID int64) error
	UpsertSpecPricing(ctx context.Context, actorUserID, specID int64, in SpecPricingInput) (*domain.ProductSpecPricing, error)
	DeleteSpecPricing(ctx context.Context, actorUserID, specID int64, cycle string) error
	// Configurable options
	OptionTree(ctx context.Context) ([]OptionGroupTree, error)
	CreateOptionGroup(ctx context.Context, actorUserID int64, in OptionGroupInput) (*domain.ConfigurableOptionGroup, error)
	UpdateOptionGroup(ctx context.Context, actorUserID, id int64, in OptionGroupInput) (*domain.ConfigurableOptionGroup, error)
	DeleteOptionGroup(ctx context.Context, actorUserID, id int64) error
	CreateOption(ctx context.Context, actorUserID, groupID int64, in OptionInput) (*domain.ConfigurableOption, error)
	UpdateOption(ctx context.Context, actorUserID, id int64, in OptionInput) (*domain.ConfigurableOption, error)
	DeleteOption(ctx context.Context, actorUserID, id int64) error
	CreateOptionValue(ctx context.Context, actorUserID, optionID int64, in OptionValueInput) (*domain.ConfigurableOptionValue, error)
	UpdateOptionValue(ctx context.Context, actorUserID, id int64, in OptionValueInput) (*domain.ConfigurableOptionValue, error)
	DeleteOptionValue(ctx context.Context, actorUserID, id int64) error
	// Coupons
	ListCoupons(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error)
	GetCoupon(ctx context.Context, id int64) (*domain.Coupon, error)
	CreateCoupon(ctx context.Context, actorUserID int64, in CouponInput) (*domain.Coupon, error)
	UpdateCoupon(ctx context.Context, actorUserID, id int64, in CouponUpdateInput) (*domain.Coupon, error)
	DeleteCoupon(ctx context.Context, actorUserID, id int64) error
}

var _ CatalogService = (*Service)(nil)

// Handler exposes the catalog HTTP endpoints.
type Handler struct {
	svc CatalogService
	mw  Middlewares
}

// NewHandler builds the catalog Handler.
func NewHandler(svc CatalogService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the catalog routes on r (the /api/v1 group). See the
// package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	publicLimit := h.mw.RateLimit("catalog", 60, time.Minute)
	r.Get("/product-groups", publicLimit, h.PublicGroups)
	r.Get("/products", publicLimit, h.PublicCatalog)
	r.Get("/products/:slug", publicLimit, h.PublicProduct)
	r.Post("/coupons/validate", publicLimit, h.ValidateCoupon)

	admin := r.Group("/admin",
		h.mw.RequireAuth(),
		h.mw.RequireRole("admin", "staff"),
		h.mw.RequirePermission("products"))

	groups := admin.Group("/product-groups")
	groups.Get("/", h.AdminListGroups)
	groups.Post("/", h.AdminCreateGroup)
	groups.Get("/:id", h.AdminGetGroup)
	groups.Patch("/:id", h.AdminUpdateGroup)
	groups.Delete("/:id", h.AdminDeleteGroup)

	products := admin.Group("/products")
	products.Get("/", h.AdminListProducts)
	products.Post("/", h.AdminCreateProduct)
	products.Get("/:id", h.AdminGetProduct)
	products.Patch("/:id", h.AdminUpdateProduct)
	products.Delete("/:id", h.AdminDeleteProduct)
	products.Post("/:id/duplicate", h.AdminDuplicateProduct)
	products.Get("/:id/pricing", h.AdminListPricing)
	products.Put("/:id/pricing", h.AdminUpsertPricing)
	products.Delete("/:id/pricing/:cycle", h.AdminDeletePricing)
	products.Get("/:id/specs", h.AdminListSpecs)
	products.Post("/:id/specs", h.AdminCreateSpec)

	specs := admin.Group("/product-specs")
	specs.Patch("/:id", h.AdminUpdateSpec)
	specs.Delete("/:id", h.AdminDeleteSpec)
	specs.Put("/:id/pricing", h.AdminUpsertSpecPricing)
	specs.Delete("/:id/pricing/:cycle", h.AdminDeleteSpecPricing)

	options := admin.Group("/config-options")
	options.Get("/", h.AdminOptionTree)
	options.Post("/groups", h.AdminCreateOptionGroup)
	options.Patch("/groups/:id", h.AdminUpdateOptionGroup)
	options.Delete("/groups/:id", h.AdminDeleteOptionGroup)
	options.Post("/groups/:id/options", h.AdminCreateOption)
	options.Patch("/options/:id", h.AdminUpdateOption)
	options.Delete("/options/:id", h.AdminDeleteOption)
	options.Post("/options/:id/values", h.AdminCreateOptionValue)
	options.Patch("/values/:id", h.AdminUpdateOptionValue)
	options.Delete("/values/:id", h.AdminDeleteOptionValue)

	coupons := admin.Group("/coupons")
	coupons.Get("/", h.AdminListCoupons)
	coupons.Post("/", h.AdminCreateCoupon)
	coupons.Get("/:id", h.AdminGetCoupon)
	coupons.Patch("/:id", h.AdminUpdateCoupon)
	coupons.Delete("/:id", h.AdminDeleteCoupon)
}

func parseID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}

func listParams(c fiber.Ctx) (httpx.Page, ports.ListParams) {
	page := httpx.ParsePage(c)
	return page, ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Search:  c.Query("search"),
		Status:  c.Query("status"),
		Sort:    c.Query("sort"),
	}
}

// Public

// PublicCatalog returns the visible catalog grouped by product group.
func (h *Handler) PublicCatalog(c fiber.Ctx) error {
	data, err := h.svc.PublicCatalog(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, data)
}

// PublicGroups returns the visible product groups.
func (h *Handler) PublicGroups(c fiber.Ctx) error {
	data, err := h.svc.PublicGroups(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, data)
}

// PublicProduct returns one visible product by slug.
func (h *Handler) PublicProduct(c fiber.Ctx) error {
	data, err := h.svc.PublicProductBySlug(c.Context(), c.Params("slug"))
	if err != nil {
		return err
	}
	return httpx.OK(c, data)
}

// ValidateCoupon validates a coupon code against a cart.
func (h *Handler) ValidateCoupon(c fiber.Ctx) error {
	var in ValidateCouponRequest
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	discount, coupon, err := h.svc.ValidateCoupon(c.Context(), in.Code, in.ProductIDs, in.Subtotal)
	if err != nil {
		return err
	}
	return httpx.OK(c, ValidateCouponResult{
		Code:      coupon.Code,
		Type:      coupon.Type,
		Value:     coupon.Value,
		Recurring: coupon.Recurring,
		Discount:  discount,
	})
}

// Admin: product groups

// AdminListGroups lists product groups (?include_hidden=true).
func (h *Handler) AdminListGroups(c fiber.Ctx) error {
	includeHidden := c.Query("include_hidden") != "false" // admin default: everything
	data, err := h.svc.ListGroups(c.Context(), includeHidden)
	if err != nil {
		return err
	}
	return httpx.OK(c, data)
}

// AdminCreateGroup creates a product group.
func (h *Handler) AdminCreateGroup(c fiber.Ctx) error {
	var in GroupInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	g, err := h.svc.CreateGroup(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, g)
}

// AdminGetGroup returns one product group.
func (h *Handler) AdminGetGroup(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	g, err := h.svc.GetGroup(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, g)
}

// AdminUpdateGroup patches a product group.
func (h *Handler) AdminUpdateGroup(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in GroupUpdateInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	g, err := h.svc.UpdateGroup(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, g)
}

// AdminDeleteGroup soft-deletes a product group (CONFLICT while products exist).
func (h *Handler) AdminDeleteGroup(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteGroup(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// Admin: products

// AdminListProducts lists products
// (?search&status=visible|hidden&group_id&type&sort).
func (h *Handler) AdminListProducts(c fiber.Ctx) error {
	page, params := listParams(c)
	var err error
	if params.GroupID, err = httpx.QueryID(c, "group_id"); err != nil {
		return err
	}
	params.Type = c.Query("type")
	rows, total, err := h.svc.ListProducts(c.Context(), params)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// AdminCreateProduct creates a product.
func (h *Handler) AdminCreateProduct(c fiber.Ctx) error {
	var in ProductInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	p, err := h.svc.CreateProduct(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, p)
}

// AdminGetProduct returns one product.
func (h *Handler) AdminGetProduct(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	p, err := h.svc.GetProduct(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// AdminUpdateProduct patches a product.
func (h *Handler) AdminUpdateProduct(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in ProductUpdateInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	p, err := h.svc.UpdateProduct(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// AdminDeleteProduct soft-deletes a product.
func (h *Handler) AdminDeleteProduct(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteProduct(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// AdminDuplicateProduct clones a product (pricing + specs included) into a
// new hidden product.
func (h *Handler) AdminDuplicateProduct(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	p, err := h.svc.DuplicateProduct(c.Context(), actor.UserID, id)
	if err != nil {
		return err
	}
	return httpx.Created(c, p)
}

// Admin: pricing

// AdminListPricing lists the pricing rows of a product.
func (h *Handler) AdminListPricing(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	rows, err := h.svc.ListPricing(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows)
}

// AdminUpsertPricing creates/updates one billing-cycle price (IDR only).
func (h *Handler) AdminUpsertPricing(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in PricingInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	pp, err := h.svc.UpsertPricing(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, pp)
}

// AdminDeletePricing removes one billing-cycle price.
func (h *Handler) AdminDeletePricing(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeletePricing(c.Context(), actor.UserID, id, c.Params("cycle")); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// Admin: dynamic-product specs

// AdminListSpecs lists a product's spec knobs with their per-cycle pricing.
func (h *Handler) AdminListSpecs(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	rows, err := h.svc.ListProductSpecs(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows)
}

// AdminCreateSpec adds a spec knob to a product.
func (h *Handler) AdminCreateSpec(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in SpecInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	sp, err := h.svc.CreateSpec(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, sp)
}

// AdminUpdateSpec replaces a spec knob.
func (h *Handler) AdminUpdateSpec(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in SpecInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	sp, err := h.svc.UpdateSpec(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, sp)
}

// AdminDeleteSpec removes a spec knob.
func (h *Handler) AdminDeleteSpec(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteSpec(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// AdminUpsertSpecPricing sets the per-unit price for one billing cycle of a spec.
func (h *Handler) AdminUpsertSpecPricing(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in SpecPricingInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	p, err := h.svc.UpsertSpecPricing(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// AdminDeleteSpecPricing removes one billing-cycle price from a spec.
func (h *Handler) AdminDeleteSpecPricing(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteSpecPricing(c.Context(), actor.UserID, id, c.Params("cycle")); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// Admin: configurable options

// AdminOptionTree returns the full option group->option->value tree.
func (h *Handler) AdminOptionTree(c fiber.Ctx) error {
	tree, err := h.svc.OptionTree(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, tree)
}

// AdminCreateOptionGroup creates an option group.
func (h *Handler) AdminCreateOptionGroup(c fiber.Ctx) error {
	var in OptionGroupInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	g, err := h.svc.CreateOptionGroup(c.Context(), actor.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, g)
}

// AdminUpdateOptionGroup updates an option group.
func (h *Handler) AdminUpdateOptionGroup(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in OptionGroupInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	g, err := h.svc.UpdateOptionGroup(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, g)
}

// AdminDeleteOptionGroup deletes an option group.
func (h *Handler) AdminDeleteOptionGroup(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteOptionGroup(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// AdminCreateOption creates an option inside a group.
func (h *Handler) AdminCreateOption(c fiber.Ctx) error {
	groupID, err := parseID(c)
	if err != nil {
		return err
	}
	var in OptionInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	o, err := h.svc.CreateOption(c.Context(), actor.UserID, groupID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, o)
}

// AdminUpdateOption updates an option.
func (h *Handler) AdminUpdateOption(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in OptionInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	o, err := h.svc.UpdateOption(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, o)
}

// AdminDeleteOption deletes an option.
func (h *Handler) AdminDeleteOption(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteOption(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// AdminCreateOptionValue creates a value under an option.
func (h *Handler) AdminCreateOptionValue(c fiber.Ctx) error {
	optionID, err := parseID(c)
	if err != nil {
		return err
	}
	var in OptionValueInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	v, err := h.svc.CreateOptionValue(c.Context(), actor.UserID, optionID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, v)
}

// AdminUpdateOptionValue updates an option value.
func (h *Handler) AdminUpdateOptionValue(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in OptionValueInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	v, err := h.svc.UpdateOptionValue(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, v)
}

// AdminDeleteOptionValue deletes an option value.
func (h *Handler) AdminDeleteOptionValue(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteOptionValue(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// Admin: coupons

// AdminListCoupons lists coupons (?search&status=active|inactive).
func (h *Handler) AdminListCoupons(c fiber.Ctx) error {
	page, params := listParams(c)
	rows, total, err := h.svc.ListCoupons(c.Context(), params)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// AdminCreateCoupon creates a coupon.
func (h *Handler) AdminCreateCoupon(c fiber.Ctx) error {
	var in CouponInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	coupon, err := h.svc.CreateCoupon(c.Context(), actor.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, coupon)
}

// AdminGetCoupon returns one coupon.
func (h *Handler) AdminGetCoupon(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	coupon, err := h.svc.GetCoupon(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, coupon)
}

// AdminUpdateCoupon patches a coupon.
func (h *Handler) AdminUpdateCoupon(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in CouponUpdateInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	coupon, err := h.svc.UpdateCoupon(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, coupon)
}

// AdminDeleteCoupon deletes a coupon.
func (h *Handler) AdminDeleteCoupon(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteCoupon(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}
