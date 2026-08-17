package adminops

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
}

var _ Middlewares = (*transporthttp.Middleware)(nil)

// AdminService is the use-case surface consumed by the handler (implemented
// by *Service).
type AdminService interface {
	Dashboard(ctx context.Context, force bool) (*DashboardData, error)
	RevenueReport(ctx context.Context, from, to time.Time, groupBy string) (*RevenueReportData, error)
	OrdersReport(ctx context.Context, from, to time.Time) (*OrdersReportData, error)
	ServicesReport(ctx context.Context) (*ServicesReportData, error)
	ListStaff(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error)
	OnlineStaff(ctx context.Context) ([]ports.PresenceEntry, error)
	CreateStaff(ctx context.Context, actorUserID int64, in CreateStaffInput) (*domain.User, error)
	GetStaff(ctx context.Context, id int64) (*domain.User, error)
	UpdateStaff(ctx context.Context, actorUserID, id int64, in UpdateStaffInput) (*domain.User, error)
	DeactivateStaff(ctx context.Context, actorUserID, id int64) (*domain.User, error)
	AuditLogs(ctx context.Context, f AuditLogFilter) ([]domain.AuditLog, int64, error)
	EmailLogs(ctx context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error)
	IntegrationLogs(ctx context.Context, f IntegrationLogFilter) ([]domain.IntegrationLog, int64, error)
	PendingModuleActions(ctx context.Context, f ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error)
	RetryModuleAction(ctx context.Context, actorUserID int64, queue, id string) error
	DeleteModuleAction(ctx context.Context, actorUserID int64, queue, id string) error
	DismissAllModuleActions(ctx context.Context, actorUserID int64, f ports.ModuleActionFilter) (int, error)
	Gateways(ctx context.Context) (*GatewaysConfig, error)
	UpdateGateways(ctx context.Context, actorUserID int64, in UpdateGatewaysInput) (*GatewaysConfig, error)
	UpdateManualGateway(ctx context.Context, actorUserID int64, in UpdateManualGatewayInput) (*ManualGatewayConfig, error)
}

var _ AdminService = (*Service)(nil)

// Handler exposes the adminops HTTP endpoints.
type Handler struct {
	svc AdminService
	mw  Middlewares
}

// NewHandler builds the adminops Handler.
func NewHandler(svc AdminService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the adminops routes on r (the /api/v1 group). See
// the package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	admin := r.Group("/admin", h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"))

	admin.Get("/dashboard", h.mw.RequirePermission("reports"), h.Dashboard)

	reports := admin.Group("/reports", h.mw.RequirePermission("reports"))
	reports.Get("/revenue", h.RevenueReport)
	reports.Get("/orders", h.OrdersReport)
	reports.Get("/services", h.ServicesReport)

	// Visible to admin+staff alike (unlike staff CRUD below) - the HostPanel
	// sidebar/dashboard "Staff Online" widget. Registered before the /staff
	// group's /:id route so the static "online" segment isn't shadowed.
	admin.Get("/staff/online", h.OnlineStaff)

	staff := admin.Group("/staff", h.mw.RequireRole("admin"))
	staff.Get("/", h.ListStaff)
	staff.Post("/", h.CreateStaff)
	staff.Get("/:id", h.GetStaff)
	staff.Patch("/:id", h.UpdateStaff)
	staff.Post("/:id/deactivate", h.DeactivateStaff)

	logs := admin.Group("/logs", h.mw.RequirePermission("logs"))
	logs.Get("/audit", h.AuditLogs)
	logs.Get("/email", h.EmailLogs)
	logs.Get("/integration", h.IntegrationLogs)
	logs.Get("/queue", h.PendingModuleActions)
	logs.Post("/queue/:queue/:id/retry", h.RetryModuleAction)
	logs.Delete("/queue/:queue/:id", h.DeleteModuleAction)
	logs.Delete("/queue", h.DismissAllModuleActions)

	gateways := admin.Group("/gateways", h.mw.RequireRole("admin"))
	gateways.Get("/", h.Gateways)
	gateways.Put("/", h.UpdateGateways)
	gateways.Put("/manual", h.UpdateManualGateway)
}

// Helpers

func parseID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}

// parseDateQuery reads an optional YYYY-MM-DD query param; zero time when absent.
func parseDateQuery(c fiber.Ctx, name string) (time.Time, error) {
	raw := c.Query(name)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(dateFormat, raw)
	if err != nil {
		return time.Time{}, apperr.Validation("invalid date",
			apperr.FieldError{Field: name, Message: "must be YYYY-MM-DD"})
	}
	return t, nil
}

func sendCSV(c fiber.Ctx, filename string, body []byte) error {
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+filename+`"`)
	return c.Send(body)
}

func wantsCSV(c fiber.Ctx) bool { return c.Query("format") == "csv" }

// Dashboard

// Dashboard returns the admin dashboard KPIs (?refresh=1 bypasses the 60s cache).
func (h *Handler) Dashboard(c fiber.Ctx) error {
	force := c.Query("refresh") == "1"
	data, err := h.svc.Dashboard(c.Context(), force)
	if err != nil {
		return err
	}
	return httpx.OK(c, data)
}

// Reports

// RevenueReport returns the revenue report (?from&to&group_by; format=csv).
func (h *Handler) RevenueReport(c fiber.Ctx) error {
	from, err := parseDateQuery(c, "from")
	if err != nil {
		return err
	}
	to, err := parseDateQuery(c, "to")
	if err != nil {
		return err
	}
	data, err := h.svc.RevenueReport(c.Context(), from, to, c.Query("group_by"))
	if err != nil {
		return err
	}
	if wantsCSV(c) {
		return sendCSV(c, "revenue_"+data.From+"_"+data.To+".csv", data.CSV())
	}
	return httpx.OK(c, data)
}

// OrdersReport returns the orders report (?from&to; format=csv).
func (h *Handler) OrdersReport(c fiber.Ctx) error {
	from, err := parseDateQuery(c, "from")
	if err != nil {
		return err
	}
	to, err := parseDateQuery(c, "to")
	if err != nil {
		return err
	}
	data, err := h.svc.OrdersReport(c.Context(), from, to)
	if err != nil {
		return err
	}
	if wantsCSV(c) {
		return sendCSV(c, "orders_"+data.From+"_"+data.To+".csv", data.CSV())
	}
	return httpx.OK(c, data)
}

// ServicesReport returns the services report (format=csv supported).
func (h *Handler) ServicesReport(c fiber.Ctx) error {
	data, err := h.svc.ServicesReport(c.Context())
	if err != nil {
		return err
	}
	if wantsCSV(c) {
		return sendCSV(c, "services.csv", data.CSV())
	}
	return httpx.OK(c, data)
}

// Staff

// ListStaff lists staff users (?search&status&page&per_page).
func (h *Handler) ListStaff(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	users, total, err := h.svc.ListStaff(c.Context(), ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Search:  c.Query("search"),
		Status:  c.Query("status"),
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, users, page.Meta(total))
}

// OnlineStaff returns admin/staff users active within the presence window.
func (h *Handler) OnlineStaff(c fiber.Ctx) error {
	entries, err := h.svc.OnlineStaff(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, entries)
}

// CreateStaff creates a staff user.
func (h *Handler) CreateStaff(c fiber.Ctx) error {
	var in CreateStaffInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	user, err := h.svc.CreateStaff(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, user)
}

// GetStaff returns one staff user.
func (h *Handler) GetStaff(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	user, err := h.svc.GetStaff(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, user)
}

// UpdateStaff patches a staff user.
func (h *Handler) UpdateStaff(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var in UpdateStaffInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	user, err := h.svc.UpdateStaff(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, user)
}

// DeactivateStaff sets a staff user inactive.
func (h *Handler) DeactivateStaff(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	user, err := h.svc.DeactivateStaff(c.Context(), actor.UserID, id)
	if err != nil {
		return err
	}
	return httpx.OK(c, user)
}

// Logs

// AuditLogs lists audit rows (?user_id&entity&action&from&to&page&per_page).
func (h *Handler) AuditLogs(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	f := AuditLogFilter{
		Entity:  c.Query("entity"),
		Action:  c.Query("action"),
		Page:    page.Page,
		PerPage: page.PerPage,
	}
	if raw := c.Query("user_id"); raw != "" {
		uid, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return apperr.Validation("invalid user_id")
		}
		f.UserID = &uid
	}
	from, err := parseDateQuery(c, "from")
	if err != nil {
		return err
	}
	if !from.IsZero() {
		f.From = &from
	}
	to, err := parseDateQuery(c, "to")
	if err != nil {
		return err
	}
	if !to.IsZero() {
		f.To = &to
	}
	rows, total, err := h.svc.AuditLogs(c.Context(), f)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// EmailLogs lists email log rows (?search&status&page&per_page).
func (h *Handler) EmailLogs(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	rows, total, err := h.svc.EmailLogs(c.Context(), ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Search:  c.Query("search"),
		Status:  c.Query("status"),
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// IntegrationLogs lists integration log rows (?provider&success&page&per_page).
func (h *Handler) IntegrationLogs(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	f := IntegrationLogFilter{
		Provider: c.Query("provider"),
		Page:     page.Page,
		PerPage:  page.PerPage,
	}
	if raw := c.Query("success"); raw != "" {
		ok, err := strconv.ParseBool(raw)
		if err != nil {
			return apperr.Validation("invalid success flag")
		}
		f.Success = &ok
	}
	rows, total, err := h.svc.IntegrationLogs(c.Context(), f)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// PendingModuleActions lists the Pending Module Actions queue
// (?type&state&page&per_page).
func (h *Handler) PendingModuleActions(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	f := ports.ModuleActionFilter{
		Type:    c.Query("type"),
		State:   c.Query("state"),
		Page:    page.Page,
		PerPage: page.PerPage,
	}
	rows, total, err := h.svc.PendingModuleActions(c.Context(), f)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// RetryModuleAction re-runs one pending module action immediately.
func (h *Handler) RetryModuleAction(c fiber.Ctx) error {
	queue, id := c.Params("queue"), c.Params("id")
	if queue == "" || id == "" {
		return apperr.Validation("queue and id are required")
	}
	identity := httpx.MustIdentity(c)
	if err := h.svc.RetryModuleAction(c.Context(), identity.UserID, queue, id); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"retried": true})
}

// DeleteModuleAction dismisses one pending module action without retrying it.
func (h *Handler) DeleteModuleAction(c fiber.Ctx) error {
	queue, id := c.Params("queue"), c.Params("id")
	if queue == "" || id == "" {
		return apperr.Validation("queue and id are required")
	}
	identity := httpx.MustIdentity(c)
	if err := h.svc.DeleteModuleAction(c.Context(), identity.UserID, queue, id); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"deleted": true})
}

// DismissAllModuleActions dismisses every module action matching the
// current ?type&state filter, without retrying any of them.
func (h *Handler) DismissAllModuleActions(c fiber.Ctx) error {
	f := ports.ModuleActionFilter{Type: c.Query("type"), State: c.Query("state")}
	identity := httpx.MustIdentity(c)
	n, err := h.svc.DismissAllModuleActions(c.Context(), identity.UserID, f)
	if err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"dismissed": n})
}

// Gateways

// Gateways returns the gateway configuration (secrets masked).
func (h *Handler) Gateways(c fiber.Ctx) error {
	cfg, err := h.svc.Gateways(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, cfg)
}

// UpdateGateways updates the non-secret gateway configuration.
func (h *Handler) UpdateGateways(c fiber.Ctx) error {
	var in UpdateGatewaysInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	cfg, err := h.svc.UpdateGateways(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, cfg)
}

// UpdateManualGateway updates the manual bank-transfer gateway config.
func (h *Handler) UpdateManualGateway(c fiber.Ctx) error {
	var in UpdateManualGatewayInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	cfg, err := h.svc.UpdateManualGateway(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, cfg)
}
