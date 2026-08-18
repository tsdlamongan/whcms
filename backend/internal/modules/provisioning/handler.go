package provisioning

import (
	"context"
	"strconv"

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
	RequireClient() fiber.Handler
}

var _ Middlewares = (*transporthttp.Middleware)(nil)

// ProvisioningService is the use-case surface consumed by the handler
// (implemented by *Service).
type ProvisioningService interface {
	// Client
	ListServices(ctx context.Context, clientID int64, p ports.ListParams) ([]ServiceView, int64, error)
	GetService(ctx context.Context, clientID, serviceID int64) (*ServiceView, error)
	ChangePassword(ctx context.Context, actorUserID, clientID, serviceID int64, password string) error
	SSO(ctx context.Context, actorUserID, clientID, serviceID int64) (string, error)
	CancelService(ctx context.Context, actorUserID, clientID, serviceID int64, in CancelServiceInput) (*domain.Service, error)
	UpgradeService(ctx context.Context, actorUserID, clientID, serviceID int64, in UpgradeServiceInput) (*UpgradeResult, error)
	// Admin services
	AdminCreateService(ctx context.Context, actorUserID int64, in AdminCreateServiceInput) (*domain.Service, error)
	AdminAction(ctx context.Context, actorUserID, serviceID int64, action, reason string, async bool) error
	AdminChangePackage(ctx context.Context, actorUserID, serviceID int64, in AdminChangePackageInput) error
	AdminUpdateService(ctx context.Context, actorUserID, serviceID int64, in AdminUpdateServiceInput) (*domain.Service, error)
	// Admin: cancellation requests
	ListCancellationRequests(ctx context.Context, p ports.ListParams) ([]CancellationRequestView, int64, error)
	AcceptCancellationRequest(ctx context.Context, actorUserID, requestID int64) error
	RejectCancellationRequest(ctx context.Context, actorUserID, requestID int64) error
	// Admin servers
	ListServers(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error)
	GetServer(ctx context.Context, id int64) (*domain.Server, error)
	CreateServer(ctx context.Context, actorUserID int64, in ServerInput) (*domain.Server, error)
	UpdateServer(ctx context.Context, actorUserID, id int64, in ServerInput) (*domain.Server, error)
	DeleteServer(ctx context.Context, actorUserID, id int64) error
	TestConnection(ctx context.Context, in TestConnectionInput) (*TestConnectionResult, error)
	// Admin server groups
	ListGroups(ctx context.Context) ([]domain.ServerGroup, error)
	GetGroup(ctx context.Context, id int64) (*domain.ServerGroup, error)
	CreateGroup(ctx context.Context, actorUserID int64, in ServerGroupInput) (*domain.ServerGroup, error)
	UpdateGroup(ctx context.Context, actorUserID, id int64, in ServerGroupInput) (*domain.ServerGroup, error)
	DeleteGroup(ctx context.Context, actorUserID, id int64) error
	ListPackages(ctx context.Context, groupID int64) (*PackageListResult, error)
}

var _ ProvisioningService = (*Service)(nil)

// Handler exposes the provisioning HTTP endpoints.
type Handler struct {
	svc ProvisioningService
	mw  Middlewares
}

// NewHandler builds the provisioning Handler.
func NewHandler(svc ProvisioningService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the provisioning routes on r (the /api/v1 group).
// See the package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	client := r.Group("/services", h.mw.RequireAuth(), h.mw.RequireClient())
	client.Get("/", h.ListMyServices)
	client.Get("/:id", h.GetMyService)
	client.Post("/:id/change-password", h.ClientChangePassword)
	client.Get("/:id/sso", h.SSO)
	client.Post("/:id/cancel", h.Cancel)
	client.Post("/:id/upgrade", h.Upgrade)

	staff := r.Group("/admin", h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"))

	services := staff.Group("/services", h.mw.RequirePermission("services"))
	services.Get("/", h.AdminListServices)
	services.Post("/", h.AdminCreateService)
	services.Get("/cancellation-requests", h.ListCancellationRequests)
	services.Post("/cancellation-requests/:id/accept", h.AcceptCancellationRequest)
	services.Post("/cancellation-requests/:id/reject", h.RejectCancellationRequest)
	services.Get("/:id", h.AdminGetService)
	services.Patch("/:id", h.AdminUpdateService)
	services.Post("/:id/create", h.adminAction(ActionCreate))
	services.Post("/:id/suspend", h.adminAction(ActionSuspend))
	services.Post("/:id/unsuspend", h.adminAction(ActionUnsuspend))
	services.Post("/:id/terminate", h.adminAction(ActionTerminate))
	services.Post("/:id/change-package", h.AdminChangePackage)
	services.Post("/:id/upgrade", h.AdminUpgrade)
	services.Post("/:id/change-password", h.AdminChangePassword)

	servers := staff.Group("/servers", h.mw.RequirePermission("servers"))
	servers.Get("/", h.ListServers)
	servers.Post("/", h.CreateServer)
	servers.Get("/:id", h.GetServer)
	servers.Patch("/:id", h.UpdateServer)
	servers.Delete("/:id", h.DeleteServer)
	servers.Post("/test-connection", h.TestConnectionPreSave)
	servers.Post("/:id/test-connection", h.TestConnection)

	groups := staff.Group("/server-groups", h.mw.RequirePermission("servers"))
	groups.Get("/", h.ListGroups)
	groups.Post("/", h.CreateGroup)
	groups.Get("/:id", h.GetGroup)
	groups.Patch("/:id", h.UpdateGroup)
	groups.Delete("/:id", h.DeleteGroup)
	groups.Get("/:id/packages", h.ListPackages)
}

func parseID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}

func listParams(c fiber.Ctx) ports.ListParams {
	page := httpx.ParsePage(c)
	return ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Search:  c.Query("search"),
		Status:  c.Query("status"),
	}
}

// Client endpoints

// ListMyServices returns the authenticated client's services.
func (h *Handler) ListMyServices(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	p := listParams(c)
	list, total, err := h.svc.ListServices(c.Context(), id.ClientID, p)
	if err != nil {
		return err
	}
	return httpx.OK(c, list, (httpx.Page{Page: p.Page, PerPage: p.Limit()}).Meta(total))
}

// GetMyService returns one of the client's services.
func (h *Handler) GetMyService(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	svc, err := h.svc.GetService(c.Context(), id.ClientID, sid)
	if err != nil {
		return err
	}
	return httpx.OK(c, svc)
}

// ClientChangePassword changes the panel password of an owned service.
func (h *Handler) ClientChangePassword(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in ChangePasswordInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.ChangePassword(c.Context(), id.UserID, id.ClientID, sid, in.Password); err != nil {
		return err
	}
	return httpx.OK(c, fiber.Map{"changed": true})
}

// SSO returns a one-time control-panel login URL.
func (h *Handler) SSO(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	url, err := h.svc.SSO(c.Context(), id.UserID, id.ClientID, sid)
	if err != nil {
		return err
	}
	return httpx.OK(c, SSOResult{URL: url})
}

// Cancel requests cancellation of an owned service.
func (h *Handler) Cancel(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in CancelServiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	svc, err := h.svc.CancelService(c.Context(), id.UserID, id.ClientID, sid, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, svc)
}

// Upgrade starts a prorated product/cycle change on an owned service.
func (h *Handler) Upgrade(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in UpgradeServiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	res, err := h.svc.UpgradeService(c.Context(), id.UserID, id.ClientID, sid, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// Admin: services

// AdminListServices lists all services with filters.
func (h *Handler) AdminListServices(c fiber.Ctx) error {
	p := listParams(c)
	list, total, err := h.svc.ListServices(c.Context(), 0, p)
	if err != nil {
		return err
	}
	return httpx.OK(c, list, (httpx.Page{Page: p.Page, PerPage: p.Limit()}).Meta(total))
}

// AdminGetService returns any service by id.
func (h *Handler) AdminGetService(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	svc, err := h.svc.GetService(c.Context(), 0, sid)
	if err != nil {
		return err
	}
	return httpx.OK(c, svc)
}

// AdminCreateService records a pre-existing hosting service on a client (no
// order, no payment, no provisioning) - POST /admin/services.
func (h *Handler) AdminCreateService(c fiber.Ctx) error {
	var in AdminCreateServiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	svc, err := h.svc.AdminCreateService(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, svc)
}

// AdminUpdateService patches a service's next_due_date and/or notes.
func (h *Handler) AdminUpdateService(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in AdminUpdateServiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	svc, err := h.svc.AdminUpdateService(c.Context(), id.UserID, sid, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, svc)
}

// Admin: cancellation requests

// ListCancellationRequests lists cancellation requests, optionally filtered
// by ?status and paginated.
func (h *Handler) ListCancellationRequests(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	p := ports.ListParams{Status: c.Query("status"), Page: page.Page, PerPage: page.PerPage}
	rows, total, err := h.svc.ListCancellationRequests(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// AcceptCancellationRequest approves a pending request.
func (h *Handler) AcceptCancellationRequest(c fiber.Ctx) error {
	rid, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.AcceptCancellationRequest(c.Context(), id.UserID, rid); err != nil {
		return err
	}
	return httpx.OK(c, fiber.Map{"accepted": true})
}

// RejectCancellationRequest denies a pending request.
func (h *Handler) RejectCancellationRequest(c fiber.Ctx) error {
	rid, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.RejectCancellationRequest(c.Context(), id.UserID, rid); err != nil {
		return err
	}
	return httpx.OK(c, fiber.Map{"rejected": true})
}

// adminAction builds the handler for one lifecycle action endpoint.
func (h *Handler) adminAction(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		sid, err := parseID(c)
		if err != nil {
			return err
		}
		var in AdminActionInput
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&in); err != nil {
				return apperr.Validation("invalid request body")
			}
		}
		id := httpx.MustIdentity(c)
		if err := h.svc.AdminAction(c.Context(), id.UserID, sid, action, in.Reason, in.Async); err != nil {
			return err
		}
		return httpx.OK(c, fiber.Map{"action": action, "async": in.Async})
	}
}

// AdminChangePackage re-binds product and/or pushes the package to the panel.
func (h *Handler) AdminChangePackage(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in AdminChangePackageInput
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&in); err != nil {
			return apperr.Validation("invalid request body")
		}
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.AdminChangePackage(c.Context(), id.UserID, sid, in); err != nil {
		return err
	}
	return httpx.OK(c, fiber.Map{"action": "change_package", "async": in.Async})
}

// AdminUpgrade runs the same prorated-diff + invoice upgrade flow as the
// client endpoint (Upgrade, above) on behalf of any client's service -
// unlike AdminChangePackage, this bills the prorated difference (or issues
// credit on a downgrade) instead of swapping the product for free. clientID
// 0 skips the ownership check inside UpgradeService/getOwned (same idiom as
// billing.Handler.AdminGetInvoice), while the invoice/audit trail still
// correctly uses the service's own client and this admin's actor id.
func (h *Handler) AdminUpgrade(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in UpgradeServiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	res, err := h.svc.UpgradeService(c.Context(), id.UserID, 0, sid, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// AdminChangePassword changes a service's panel password (any client).
func (h *Handler) AdminChangePassword(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in AdminChangePasswordInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.ChangePassword(c.Context(), id.UserID, 0, sid, in.Password); err != nil {
		return err
	}
	return httpx.OK(c, fiber.Map{"changed": true})
}

// Admin: servers

// ListServers lists provisioning servers.
func (h *Handler) ListServers(c fiber.Ctx) error {
	p := listParams(c)
	list, total, err := h.svc.ListServers(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.OK(c, list, (httpx.Page{Page: p.Page, PerPage: p.Limit()}).Meta(total))
}

// GetServer returns one server.
func (h *Handler) GetServer(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	server, err := h.svc.GetServer(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, server)
}

// CreateServer creates a server.
func (h *Handler) CreateServer(c fiber.Ctx) error {
	var in ServerInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	server, err := h.svc.CreateServer(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, server)
}

// UpdateServer updates a server.
func (h *Handler) UpdateServer(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	var in ServerInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	server, err := h.svc.UpdateServer(c.Context(), id.UserID, sid, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, server)
}

// DeleteServer removes a server.
func (h *Handler) DeleteServer(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.DeleteServer(c.Context(), id.UserID, sid); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// TestConnection probes a stored server (secrets loaded from the row) with a
// read-only connectivity check.
func (h *Handler) TestConnection(c fiber.Ctx) error {
	sid, err := parseID(c)
	if err != nil {
		return err
	}
	res, err := h.svc.TestConnection(c.Context(), TestConnectionInput{ID: sid})
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// TestConnectionPreSave probes a server from the current form values before it
// is saved (WHMCS-style "Test Connection"), returning metadata the UI can
// auto-populate. An optional id fills blank secrets from the stored row.
func (h *Handler) TestConnectionPreSave(c fiber.Ctx) error {
	var in TestConnectionInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	res, err := h.svc.TestConnection(c.Context(), in)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// Admin: server groups

// ListGroups returns all server groups.
func (h *Handler) ListGroups(c fiber.Ctx) error {
	groups, err := h.svc.ListGroups(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, groups)
}

// GetGroup returns one server group.
func (h *Handler) GetGroup(c fiber.Ctx) error {
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

// CreateGroup creates a server group.
func (h *Handler) CreateGroup(c fiber.Ctx) error {
	var in ServerGroupInput
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

// UpdateGroup updates a server group.
func (h *Handler) UpdateGroup(c fiber.Ctx) error {
	gid, err := parseID(c)
	if err != nil {
		return err
	}
	var in ServerGroupInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	g, err := h.svc.UpdateGroup(c.Context(), id.UserID, gid, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, g)
}

// DeleteGroup removes a server group.
func (h *Handler) DeleteGroup(c fiber.Ctx) error {
	gid, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.DeleteGroup(c.Context(), id.UserID, gid); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// ListPackages previews packages defined on a representative server in the
// group, for the admin product form's package-name picker.
func (h *Handler) ListPackages(c fiber.Ctx) error {
	gid, err := parseID(c)
	if err != nil {
		return err
	}
	res, err := h.svc.ListPackages(c.Context(), gid)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}
