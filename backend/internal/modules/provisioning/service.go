// Package provisioning implements the M-PROVISIONING module: server and
// server-group management, service lifecycle (provision create / suspend /
// unsuspend / terminate / change package / change password), renewals,
// prorated upgrades/downgrades, client cancellation and the auto-suspend /
// auto-terminate automation crons.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	GET    /services                                  client service list        [client]
//	GET    /services/:id                              service detail             [client]
//	POST   /services/:id/change-password              sync panel password change [client]
//	GET    /services/:id/sso                          one-time panel login URL   [client]
//	POST   /services/:id/cancel                       {mode: immediate|end_of_term} [client]
//	POST   /services/:id/upgrade                      {product_id, cycle, specs?} prorated [client]
//	GET    /admin/services                                                        [perm: services]
//	GET    /admin/services/cancellation-requests      ?status&page&per_page       [perm: services]
//	POST   /admin/services/cancellation-requests/:id/accept                      [perm: services]
//	POST   /admin/services/cancellation-requests/:id/reject                      [perm: services]
//	GET    /admin/services/:id                                                    [perm: services]
//	POST   /admin/services/:id/create                 {async?}                    [perm: services]
//	POST   /admin/services/:id/suspend                {reason?, async?}           [perm: services]
//	POST   /admin/services/:id/unsuspend              {async?}                    [perm: services]
//	POST   /admin/services/:id/terminate              {async?}                    [perm: services]
//	POST   /admin/services/:id/change-package         {product_id?, async?}       [perm: services]
//	POST   /admin/services/:id/upgrade                {product_id, cycle, specs?} prorated [perm: services]
//	POST   /admin/services/:id/change-password        {password}                  [perm: services]
//	GET    /admin/servers                                                         [perm: servers]
//	POST   /admin/servers                                                         [perm: servers]
//	GET    /admin/servers/:id                                                     [perm: servers]
//	PATCH  /admin/servers/:id                                                     [perm: servers]
//	DELETE /admin/servers/:id                                                     [perm: servers]
//	POST   /admin/servers/test-connection             pre-save probe (form body)  [perm: servers]
//	POST   /admin/servers/:id/test-connection         saved-server probe (sync)   [perm: servers]
//	GET    /admin/server-groups                                                   [perm: servers]
//	POST   /admin/server-groups                                                   [perm: servers]
//	GET    /admin/server-groups/:id                                               [perm: servers]
//	PATCH  /admin/server-groups/:id                                               [perm: servers]
//	DELETE /admin/server-groups/:id                                               [perm: servers]
//
// Worker entrypoints: ProvisionCreate, ProvisionSuspend, ProvisionUnsuspend,
// ProvisionTerminate, ProvisionChangePackage, ProvisionChangePassword,
// AutoSuspend, AutoTerminate, NotifyProvisionFailure.
// Cross-service (ports.ServiceRenewer): RenewService, ApplyUpgrade.
package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Settings keys and defaults used by the automation crons.
const (
	settingSuspendAfterDays   = "automation.suspend_after_days"
	settingTerminateAfterDays = "automation.terminate_after_days"
	settingInvoiceDueDays     = "billing.invoice_due_days"

	defaultSuspendAfterDays   = 7
	defaultTerminateAfterDays = 21
	defaultInvoiceDueDays     = 3
)

// panel_meta JSON keys owned by this module.
const (
	metaSuspendedAt       = "suspended_at"
	metaCancelAtPeriodEnd = "cancel_at_period_end"
	metaAccountIP         = "account_ip"
)

// suspendReasonOverdue is the reason used by cron:auto_suspend.
const suspendReasonOverdue = "Overdue on payment"

// Admin service action names (AdminAction).
const (
	ActionCreate    = "create"
	ActionSuspend   = "suspend"
	ActionUnsuspend = "unsuspend"
	ActionTerminate = "terminate"
)

// Consumer-side interfaces (implemented elsewhere, wired by composition root)

// ServiceStore is the services persistence surface: ports.ServiceRepo plus
// the automation queries implemented by *Repo.
type ServiceStore interface {
	ports.ServiceRepo
	// ListOverdueSuspendable returns active services with an overdue renewal
	// invoice whose due_date <= before.
	ListOverdueSuspendable(ctx context.Context, before time.Time) ([]domain.Service, error)
	// ListTerminatable returns suspended-too-long or cancel_at_period_end
	// services due for termination.
	ListTerminatable(ctx context.Context, suspendedBefore, dueBefore time.Time) ([]domain.Service, error)
	// CountByServerAndPackage counts non-terminal services on a server whose
	// panel_meta.package_name matches name, excluding excludeServiceID. Lets
	// ProvisionTerminate confirm no sibling service still needs a shared
	// dynamic package before deleting it.
	CountByServerAndPackage(ctx context.Context, serverID int64, packageName string, excludeServiceID int64) (int64, error)
}

// ServerStore mirrors ports.ServerRepo with collision-free method names
// (implemented by *Repo, which also implements ports.ServiceRepo).
type ServerStore interface {
	CreateServer(ctx context.Context, s *domain.Server) error
	GetServerByID(ctx context.Context, id int64) (*domain.Server, error)
	UpdateServer(ctx context.Context, s *domain.Server) error
	ListServers(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error)
	DeleteServer(ctx context.Context, id int64) error
	CreateGroup(ctx context.Context, g *domain.ServerGroup) error
	GetGroupByID(ctx context.Context, id int64) (*domain.ServerGroup, error)
	UpdateGroup(ctx context.Context, g *domain.ServerGroup) error
	ListGroups(ctx context.Context) ([]domain.ServerGroup, error)
	DeleteGroup(ctx context.Context, id int64) error
	PickServer(ctx context.Context, groupID int64) (*domain.Server, error)
}

// ProductStore is the catalog read surface (subset of ports.ProductRepo).
type ProductStore interface {
	GetByID(ctx context.Context, id int64) (*domain.Product, error)
	GetPricing(ctx context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error)
	ListSpecs(ctx context.Context, productID int64) ([]domain.ProductSpec, error)
	GetSpecPricing(ctx context.Context, specID int64, cycle domain.BillingCycle) (*domain.ProductSpecPricing, error)
}

// ClientStore reads client profiles (subset of ports.ClientRepo).
type ClientStore interface {
	GetByID(ctx context.Context, id int64) (*domain.Client, error)
}

// UserStore reads user accounts (subset of ports.UserRepo).
type UserStore interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

// InvoiceStore is the invoice surface needed to cancel open renewal invoices
// (subset of ports.InvoiceRepo).
type InvoiceStore interface {
	GetItems(ctx context.Context, invoiceID int64) ([]domain.InvoiceItem, error)
	GetItemsByInvoiceIDs(ctx context.Context, invoiceIDs []int64) (map[int64][]domain.InvoiceItem, error)
	ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error)
	UpdateStatus(ctx context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error
}

// CreditAdder credits a client's account (MODULES.md §2, implemented by the
// clients module service).
type CreditAdder interface {
	AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error
}

// Service

// Deps are the provisioning service dependencies (wired by composition root).
type Deps struct {
	Services ServiceStore                  // *provisioning.Repo
	Servers  ServerStore                   // *provisioning.Repo
	Products ProductStore                  // catalog repo
	Clients  ClientStore                   // clients repo
	Users    UserStore                     // users repo (auth module)
	Invoices InvoiceStore                  // invoice repo (billing module)
	Modules  map[string]ports.ServerModule // keyed by module name: "cpanel", "directadmin"
	Crypt    ports.Encryptor
	Queue    ports.Enqueuer
	Notify   ports.NotificationSender
	Billing  ports.InvoiceCreator // upgrade diff invoices
	Credit   CreditAdder          // downgrade excess -> account credit
	Settings ports.SettingsRepo
	Tx       ports.TxManager
	Audit    ports.AuditLogger
	Clock    ports.Clock

	CancellationRequests ports.CancellationRequestRepo
}

// Service implements the provisioning use-cases.
type Service struct {
	d   Deps
	val *validate.Validator
}

// New builds the provisioning Service.
func New(d Deps) *Service {
	return &Service{d: d, val: validate.New()}
}

// Compile-time cross-service port check.
var _ ports.ServiceRenewer = (*Service)(nil)

// Shared helpers

// wrap passes apperr errors through and wraps everything else as INTERNAL.
func wrap(err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	return apperr.Internal(err)
}

// getOwned loads a service and enforces client ownership. clientID 0 (staff /
// worker) bypasses the check. Foreign services return NOT_FOUND (never 403).
func (s *Service) getOwned(ctx context.Context, clientID, serviceID int64) (*domain.Service, error) {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return nil, wrap(err)
	}
	if clientID != 0 && svc.ClientID != clientID {
		return nil, apperr.NotFound("service")
	}
	return svc, nil
}

// moduleFor returns the server adapter for a module name; nil (no error) for
// module "none"/empty, INTERNAL when the adapter is not wired.
func (s *Service) moduleFor(name domain.ServerModuleName) (ports.ServerModule, error) {
	if name == domain.ModuleNone || name == "" {
		return nil, nil
	}
	m, ok := s.d.Modules[string(name)]
	if !ok {
		return nil, apperr.Internal(fmt.Errorf("provisioning: server module %q not configured", name))
	}
	return m, nil
}

// serverConfig builds the decrypted ports.ServerConfig for a server row.
func (s *Service) serverConfig(sv *domain.Server) (ports.ServerConfig, error) {
	cfg := ports.ServerConfig{
		ID:          sv.ID,
		Name:        sv.Name,
		Module:      sv.Module,
		Hostname:    sv.Hostname,
		Port:        sv.Port,
		Username:    sv.Username,
		UseSSL:      sv.UseSSL,
		Nameserver1: sv.Nameserver1,
		Nameserver2: sv.Nameserver2,
	}
	if sv.PasswordEnc != "" {
		p, err := s.d.Crypt.Decrypt(sv.PasswordEnc)
		if err != nil {
			return cfg, apperr.Internal(fmt.Errorf("provisioning: decrypt server password: %w", err))
		}
		cfg.Password = p
	}
	if sv.APITokenEnc != "" {
		t, err := s.d.Crypt.Decrypt(sv.APITokenEnc)
		if err != nil {
			return cfg, apperr.Internal(fmt.Errorf("provisioning: decrypt server api token: %w", err))
		}
		cfg.APIToken = t
	}
	return cfg, nil
}

// panelContext resolves the adapter + decrypted server config for a service.
// ok=false (no error) when the service has no control panel (module none or
// no server assigned).
func (s *Service) panelContext(ctx context.Context, svc *domain.Service) (ports.ServerModule, ports.ServerConfig, bool, error) {
	product, err := s.d.Products.GetByID(ctx, svc.ProductID)
	if err != nil {
		return nil, ports.ServerConfig{}, false, wrap(err)
	}
	mod, err := s.moduleFor(product.Module)
	if err != nil {
		return nil, ports.ServerConfig{}, false, err
	}
	if mod == nil || svc.ServerID == nil {
		return nil, ports.ServerConfig{}, false, nil
	}
	server, err := s.d.Servers.GetServerByID(ctx, *svc.ServerID)
	if err != nil {
		return nil, ports.ServerConfig{}, false, wrap(err)
	}
	cfg, err := s.serverConfig(server)
	if err != nil {
		return nil, ports.ServerConfig{}, false, err
	}
	return mod, cfg, true, nil
}

// chosenSpec is the snapshot of one customer-chosen dynamic spec stored in
// services.panel_meta[chosen_specs] by order activation.
type chosenSpec struct {
	Key          string `json:"key"`
	ProvisionKey string `json:"provision_key"`
	Unit         string `json:"unit"`
	Qty          int64  `json:"qty"`
	Unlimited    bool   `json:"unlimited"`
}

// buildPackageSpec assembles the control-panel package from the chosen specs
// snapshot, using the current spec definitions as authoritative for each
// knob's provision key and unit. Sizes are converted to MB; unlimited choices
// carry the ports.Unlimited sentinel. Panel toggles (shell/CGI access,
// FeatureList, TemplatePackage) come straight from product. The resulting
// Name is a deterministic hash of the resolved limits and toggles
// (domain.DynamicPackageName), not the service ID - so any other service
// resolving to identical limits and toggles on the same server shares this
// same package via EnsurePackage's idempotent create-or-update. packagePrefix
// is the owning server's PackagePrefix.
func (s *Service) buildPackageSpec(ctx context.Context, svc *domain.Service, product *domain.Product, packagePrefix string) (ports.PackageSpec, error) {
	spec := ports.PackageSpec{
		Limits:          map[domain.ProvisionKey]int64{},
		FeatureList:     product.FeatureList,
		ShellAccess:     product.ShellAccess,
		CGIAccess:       product.CGIAccess,
		TemplatePackage: product.TemplatePackage,
	}
	raw, ok := metaMap(svc.PanelMeta)[domain.PanelMetaChosenSpecs]
	if ok {
		b, err := json.Marshal(raw)
		if err != nil {
			return spec, apperr.Internal(err)
		}
		var chosen []chosenSpec
		if err := json.Unmarshal(b, &chosen); err != nil {
			return spec, apperr.Internal(err)
		}

		defs, err := s.d.Products.ListSpecs(ctx, product.ID)
		if err != nil {
			return spec, wrap(err)
		}
		byKey := make(map[string]domain.ProductSpec, len(defs))
		for _, d := range defs {
			byKey[d.Key] = d
		}
		for _, cs := range chosen {
			pk := domain.ProvisionKey(cs.ProvisionKey)
			unit := domain.SpecUnit(cs.Unit)
			if def, ok := byKey[cs.Key]; ok {
				pk, unit = def.ProvisionKey, def.Unit
			}
			if !pk.Valid() {
				continue
			}
			if cs.Unlimited {
				spec.Limits[pk] = ports.Unlimited
			} else {
				spec.Limits[pk] = domain.ToPanelMB(cs.Qty, unit)
			}
		}
	}
	// no specs chosen - package uses panel defaults, still named deterministically
	spec.Name = domain.DynamicPackageName(packagePrefix, spec.Limits, domain.PackageToggles{
		FeatureList:     spec.FeatureList,
		ShellAccess:     spec.ShellAccess,
		CGIAccess:       spec.CGIAccess,
		TemplatePackage: spec.TemplatePackage,
	})
	return spec, nil
}

// metaMap decodes panel_meta into a mutable map (empty map when nil/invalid).
func metaMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func marshalMeta(m map[string]any) json.RawMessage {
	raw, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// metaBool reads a boolean flag from panel_meta.
func metaBool(raw json.RawMessage, key string) bool {
	v, _ := metaMap(raw)[key].(bool)
	return v
}

// today returns the clock date truncated to midnight UTC (DATE columns).
func (s *Service) today() time.Time {
	t := s.d.Clock.Now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// notifyService best-effort sends a service lifecycle template to the
// service's owner. Failures are swallowed: notification must never fail the
// provisioning operation itself.
func (s *Service) notifyService(ctx context.Context, svc *domain.Service, key string, data map[string]any) {
	client, err := s.d.Clients.GetByID(ctx, svc.ClientID)
	if err != nil || client == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["Domain"]; !ok {
		data["Domain"] = svc.Domain
	}
	_ = s.d.Notify.SendTemplate(ctx, client.UserID, key, data)
}

// panelURL builds the customer panel login URL for a server.
func panelURL(module domain.ServerModuleName, hostname string) string {
	switch module {
	case domain.ModuleCpanel:
		return "https://" + hostname + ":2083"
	case domain.ModuleDirectAdmin:
		return "https://" + hostname + ":2222"
	default:
		return ""
	}
}

// Provision lifecycle (worker + admin sync entrypoints)

// ProvisionCreate creates the hosting account for a pending service.
// Idempotent: any status other than pending is a no-op (nil). Adapter errors
// propagate so asynq retries; the worker calls NotifyProvisionFailure on the
// final retry.
func (s *Service) ProvisionCreate(ctx context.Context, serviceID int64) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}
	if svc.Status != domain.ServicePending {
		return nil // already provisioned (or ended) - idempotent
	}
	if _, err := domain.TransitionService(svc.Status, domain.ServiceActive); err != nil {
		return err
	}

	product, err := s.d.Products.GetByID(ctx, svc.ProductID)
	if err != nil {
		return wrap(err)
	}
	mod, err := s.moduleFor(product.Module)
	if err != nil {
		return err
	}

	welcomeData := map[string]any{
		"ServiceName": product.Name,
		"Domain":      svc.Domain,
		"Username":    svc.Username,
	}

	if mod != nil {
		// Pick and pin a server before calling the adapter so retries reuse it.
		if svc.ServerID == nil {
			if product.ServerGroupID == nil {
				return apperr.Conflict("product has no server group configured")
			}
			server, err := s.d.Servers.PickServer(ctx, *product.ServerGroupID)
			if err != nil {
				return wrap(err)
			}
			svc.ServerID = &server.ID
			if err := s.d.Services.Update(ctx, svc); err != nil {
				return wrap(err)
			}
		}
		server, err := s.d.Servers.GetServerByID(ctx, *svc.ServerID)
		if err != nil {
			return wrap(err)
		}
		cfg, err := s.serverConfig(server)
		if err != nil {
			return err
		}

		password := ""
		if svc.PasswordEnc != "" {
			password, err = s.d.Crypt.Decrypt(svc.PasswordEnc)
			if err != nil {
				return apperr.Internal(fmt.Errorf("provisioning: decrypt service password: %w", err))
			}
		}
		email := ""
		if client, err := s.d.Clients.GetByID(ctx, svc.ClientID); err == nil && client != nil {
			if user, err := s.d.Users.GetByID(ctx, client.UserID); err == nil && user != nil {
				email = user.Email
			}
		}

		// For configurable products, build the per-service package from the
		// chosen specs and create it on the panel before the account (Option A).
		packageName := product.PackageName
		var pkgSpec ports.PackageSpec
		if product.Configurable {
			pkgSpec, err = s.buildPackageSpec(ctx, svc, product, server.PackagePrefix)
			if err != nil {
				return err
			}
			if err := mod.EnsurePackage(ctx, cfg, pkgSpec); err != nil {
				return wrap(err) // stays pending; asynq retries
			}
			packageName = pkgSpec.Name
		}

		result, err := mod.Create(ctx, cfg, ports.CreateAccountParams{
			Username: svc.Username,
			Domain:   svc.Domain,
			Password: password,
			Package:  packageName,
			Email:    email,
			IP:       server.IPAddress,
		})
		if err != nil {
			if apperr.From(err).Code != apperr.CodeConflict {
				return wrap(err) // stays pending; asynq retries
			}
			// The panel account may already exist from an earlier attempt that
			// created it but failed before this job persisted ServiceActive
			// (e.g. a crash or DB error right after Create) - asynq then retries
			// with the same deterministic username, which would conflict forever.
			// Confirm it's really our account (same domain) before treating the
			// conflict as the already-completed step; otherwise it's a genuine
			// collision and must surface.
			info, infoErr := mod.AccountInfo(ctx, cfg, svc.Username)
			if infoErr == nil && strings.EqualFold(info.Domain, svc.Domain) {
				result = &ports.AccountResult{Username: info.Username, Domain: info.Domain, Meta: info.Meta}
			} else {
				// Genuine collision: some other account already owns this
				// username (e.g. another service's domain shares the same
				// UsernameFromDomain label). Give this service a distinct,
				// deterministic candidate and let asynq's retry pick it up -
				// otherwise it would fail forever proposing the same taken name.
				svc.Username = domain.DisambiguateUsername(svc.Username, svc.ID)
				if updErr := s.d.Services.Update(ctx, svc); updErr != nil {
					return wrap(updErr)
				}
				return wrap(err) // stays pending; asynq retries with the new username
			}
		}

		meta := metaMap(svc.PanelMeta)
		if product.Configurable {
			meta[domain.PanelMetaPackageName] = pkgSpec.Name
			meta[domain.PanelMetaLimits] = pkgSpec.Limits
		}
		if result != nil {
			if result.Username != "" && result.Username != svc.Username {
				svc.Username = result.Username
			}
			if result.IP != "" {
				meta[metaAccountIP] = result.IP
			}
			for k, v := range result.Meta {
				meta[k] = v
			}
		}
		svc.PanelMeta = marshalMeta(meta)

		welcomeData["Username"] = svc.Username
		welcomeData["Password"] = password
		welcomeData["PanelURL"] = panelURL(server.Module, server.Hostname)
	}

	svc.Status = domain.ServiceActive
	regDate := s.today()
	svc.RegistrationDate = &regDate
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	templateKey := product.WelcomeEmailTemplate
	if templateKey == "" {
		templateKey = "service_activated"
	}
	s.notifyService(ctx, svc, templateKey, welcomeData)

	s.d.Audit.Log(ctx, 0, "service.provision_create", "service", svc.ID, nil,
		map[string]any{"status": svc.Status, "server_id": svc.ServerID})
	return nil
}

// ProvisionSuspend suspends a service's hosting account. Idempotent: already
// suspended -> nil. Invalid transitions (pending/terminated/...) -> CONFLICT.
func (s *Service) ProvisionSuspend(ctx context.Context, serviceID int64, reason string) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}
	if svc.Status == domain.ServiceSuspended {
		return nil
	}
	if _, err := domain.TransitionService(svc.Status, domain.ServiceSuspended); err != nil {
		return err
	}

	mod, cfg, hasPanel, err := s.panelContext(ctx, svc)
	if err != nil {
		return err
	}
	if hasPanel {
		if err := mod.Suspend(ctx, cfg, svc.Username, reason); err != nil {
			return wrap(err)
		}
	}

	svc.Status = domain.ServiceSuspended
	svc.SuspendReason = reason
	meta := metaMap(svc.PanelMeta)
	meta[metaSuspendedAt] = s.d.Clock.Now().UTC().Format(time.RFC3339)
	svc.PanelMeta = marshalMeta(meta)
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	s.notifyService(ctx, svc, "service_suspended", map[string]any{
		"ServiceName": svc.Domain, "Reason": reason,
	})
	s.d.Audit.Log(ctx, 0, "service.suspend", "service", svc.ID, nil,
		map[string]any{"reason": reason})
	return nil
}

// ProvisionUnsuspend restores a suspended service. Idempotent: already
// active -> nil.
func (s *Service) ProvisionUnsuspend(ctx context.Context, serviceID int64) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}
	if svc.Status == domain.ServiceActive {
		return nil
	}
	if _, err := domain.TransitionService(svc.Status, domain.ServiceActive); err != nil {
		return err
	}

	mod, cfg, hasPanel, err := s.panelContext(ctx, svc)
	if err != nil {
		return err
	}
	if hasPanel {
		if err := mod.Unsuspend(ctx, cfg, svc.Username); err != nil {
			return wrap(err)
		}
	}

	svc.Status = domain.ServiceActive
	svc.SuspendReason = ""
	meta := metaMap(svc.PanelMeta)
	delete(meta, metaSuspendedAt)
	svc.PanelMeta = marshalMeta(meta)
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	s.notifyService(ctx, svc, "service_unsuspended", map[string]any{"ServiceName": svc.Domain})
	s.d.Audit.Log(ctx, 0, "service.unsuspend", "service", svc.ID, nil, nil)
	return nil
}

// ProvisionTerminate removes a service's hosting account. Idempotent:
// terminated/cancelled -> nil. A still-pending service is cancelled instead
// (nothing was provisioned).
func (s *Service) ProvisionTerminate(ctx context.Context, serviceID int64) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}
	switch svc.Status {
	case domain.ServiceTerminated, domain.ServiceCancelled:
		return nil
	case domain.ServicePending:
		// Nothing provisioned yet: pending -> cancelled.
		if _, err := domain.TransitionService(svc.Status, domain.ServiceCancelled); err != nil {
			return err
		}
		svc.Status = domain.ServiceCancelled
		now := s.d.Clock.Now().UTC()
		svc.TerminatedAt = &now
		if err := s.d.Services.Update(ctx, svc); err != nil {
			return wrap(err)
		}
		s.d.Audit.Log(ctx, 0, "service.cancel", "service", svc.ID, nil, nil)
		s.resolvePendingCancellation(ctx, svc.ID)
		return nil
	}
	if _, err := domain.TransitionService(svc.Status, domain.ServiceTerminated); err != nil {
		return err
	}

	mod, cfg, hasPanel, err := s.panelContext(ctx, svc)
	if err != nil {
		return err
	}
	if hasPanel {
		if err := mod.Terminate(ctx, cfg, svc.Username); err != nil {
			return wrap(err)
		}
	}

	// Persist the terminated status BEFORE any package cleanup: the account is
	// already gone from the panel, so a cleanup failure must never leave the
	// service recorded as active and the job retrying (re-terminating a
	// removed account is a no-op, but the inconsistent window is avoidable).
	svc.Status = domain.ServiceTerminated
	now := s.d.Clock.Now().UTC()
	svc.TerminatedAt = &now
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	if hasPanel {
		pkg, _ := metaMap(svc.PanelMeta)[domain.PanelMetaPackageName].(string)
		s.cleanupDynamicPackage(ctx, svc, mod, cfg, pkg)
	}

	s.notifyService(ctx, svc, "service_terminated", map[string]any{"ServiceName": svc.Domain})
	s.d.Audit.Log(ctx, 0, "service.terminate", "service", svc.ID, nil, nil)
	s.resolvePendingCancellation(ctx, svc.ID)
	return nil
}

// resolvePendingCancellation marks the service's pending cancellation
// request (if any) CancellationAutoProcessed now that termination has
// actually completed - closing the window where a client's immediate-mode
// request stays pending (and so blocks a duplicate submission via the "one
// pending request per service" guard in CancelService) for as long as the
// job sits in the queue. Best-effort: a lookup/update failure here must not
// fail the termination itself, which already succeeded.
func (s *Service) resolvePendingCancellation(ctx context.Context, serviceID int64) {
	cr, err := s.d.CancellationRequests.GetPendingByService(ctx, serviceID)
	if err != nil || cr == nil {
		return
	}
	now := s.d.Clock.Now().UTC()
	cr.Status = domain.CancellationAutoProcessed
	cr.DecidedAt = &now
	_ = s.d.CancellationRequests.Update(ctx, cr)
}

// cleanupDynamicPackage best-effort deletes a dynamic (custom-spec) panel
// package the service no longer needs, once BOTH guards confirm nothing else
// uses it: no other non-terminal service row on this server (DB-side,
// CountByServerAndPackage) and no account on the panel itself (panel-side,
// ServerModule.PackageInUse - which also catches accounts created outside
// this app, or the same physical host registered as a second servers row).
// Deliberately returns nothing: the caller's own operation (terminate /
// package change) has already succeeded and been persisted, so a cleanup
// problem must never fail or retry the job - the operator is alerted instead
// and can delete the stranded package manually.
func (s *Service) cleanupDynamicPackage(ctx context.Context, svc *domain.Service, mod ports.ServerModule, cfg ports.ServerConfig, pkg string) {
	if pkg == "" || svc.ServerID == nil {
		return
	}
	alert := func(detail string) {
		subject := fmt.Sprintf("Package cleanup needs attention: %s (service #%d)", pkg, svc.ID)
		_ = s.d.Notify.AlertAdmin(ctx, subject, detail)
	}
	others, err := s.d.Services.CountByServerAndPackage(ctx, *svc.ServerID, pkg, svc.ID)
	if err != nil {
		alert(fmt.Sprintf("could not count sibling services for package %q on server %d: %v - the package was left in place, delete it manually once confirmed unused", pkg, *svc.ServerID, err))
		return
	}
	if others > 0 {
		return // still referenced by another service - expected, keep it
	}
	inUse, err := mod.PackageInUse(ctx, cfg, pkg)
	if err != nil {
		alert(fmt.Sprintf("panel-side in-use check failed for package %q: %v - the package was left in place, delete it manually once confirmed unused", pkg, err))
		return
	}
	if inUse {
		alert(fmt.Sprintf("package %q is still used by a panel account this app does not track - deletion skipped; review the server for out-of-band accounts", pkg))
		return
	}
	if err := mod.DeletePackage(ctx, cfg, pkg); err != nil {
		alert(fmt.Sprintf("deleting package %q failed: %v - delete it manually", pkg, err))
	}
}

// ProvisionChangePackage pushes the service's current product package to the
// control panel (used after ApplyUpgrade and by the admin change-package
// action). For configurable (custom-spec) products the per-service package is
// first rebuilt from the chosen_specs snapshot in panel_meta (EnsurePackage,
// same as ProvisionCreate), then the account is moved onto it; a dynamic
// package left behind with no remaining services is deleted (same rule as
// terminate) so resizes don't strand near-duplicate spec packages.
func (s *Service) ProvisionChangePackage(ctx context.Context, serviceID int64) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}
	product, err := s.d.Products.GetByID(ctx, svc.ProductID)
	if err != nil {
		return wrap(err)
	}

	mod, cfg, hasPanel, err := s.panelContext(ctx, svc)
	if err != nil {
		return err
	}
	packageName := product.PackageName
	if hasPanel {
		meta := metaMap(svc.PanelMeta)
		oldPkg, _ := meta[domain.PanelMetaPackageName].(string)

		if product.Configurable {
			server, err := s.d.Servers.GetServerByID(ctx, *svc.ServerID)
			if err != nil {
				return wrap(err)
			}
			pkgSpec, err := s.buildPackageSpec(ctx, svc, product, server.PackagePrefix)
			if err != nil {
				return err
			}
			if err := mod.EnsurePackage(ctx, cfg, pkgSpec); err != nil {
				return wrap(err) // asynq retries
			}
			packageName = pkgSpec.Name
			meta[domain.PanelMetaPackageName] = pkgSpec.Name
			meta[domain.PanelMetaLimits] = pkgSpec.Limits
		} else {
			// Moving (back) to a flat product: the dynamic-package bookkeeping
			// no longer describes this service.
			delete(meta, domain.PanelMetaPackageName)
			delete(meta, domain.PanelMetaLimits)
		}

		if err := mod.ChangePackage(ctx, cfg, svc.Username, packageName); err != nil {
			return wrap(err)
		}
		svc.PanelMeta = marshalMeta(meta)
		if err := s.d.Services.Update(ctx, svc); err != nil {
			return wrap(err)
		}

		// Best-effort: the package change itself already succeeded and was
		// persisted, so cleaning up the dynamic package left behind must not
		// fail or retry the job.
		if oldPkg != "" && oldPkg != packageName {
			s.cleanupDynamicPackage(ctx, svc, mod, cfg, oldPkg)
		}
	}

	s.d.Audit.Log(ctx, 0, "service.change_package", "service", svc.ID, nil,
		map[string]any{"package": packageName})
	return nil
}

// ProvisionChangePassword changes the panel password (worker variant of
// ChangePassword: no ownership check).
func (s *Service) ProvisionChangePassword(ctx context.Context, serviceID int64, password string) error {
	return s.changePassword(ctx, 0, 0, serviceID, password)
}

// NotifyProvisionFailure alerts the operator after a provisioning job
// exhausted its retries (called by the worker's final-retry hook).
func (s *Service) NotifyProvisionFailure(ctx context.Context, serviceID int64, taskType, errMsg string) error {
	detail := fmt.Sprintf("task=%s service_id=%d error=%s", taskType, serviceID, errMsg)
	if svc, err := s.d.Services.GetByID(ctx, serviceID); err == nil && svc != nil {
		detail = fmt.Sprintf("task=%s service_id=%d domain=%s client_id=%d status=%s error=%s",
			taskType, serviceID, svc.Domain, svc.ClientID, svc.Status, errMsg)
	}
	subject := fmt.Sprintf("Provisioning failed: %s (service #%d)", taskType, serviceID)
	if err := s.d.Notify.AlertAdmin(ctx, subject, detail); err != nil {
		return wrap(err)
	}
	return nil
}

// Cross-service: ports.ServiceRenewer (consumed by billing.ProcessPaid)

// RenewService advances next_due_date by one billing cycle after a paid
// renewal invoice and unsuspends a suspended service.
func (s *Service) RenewService(ctx context.Context, serviceID int64) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}

	base := s.today()
	if svc.NextDueDate != nil {
		base = *svc.NextDueDate
	}
	next := domain.AddCycle(base, svc.BillingCycle)
	svc.NextDueDate = &next
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	if svc.Status == domain.ServiceSuspended {
		if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionUnsuspend,
			jobs.ProvisionUnsuspendPayload{ServiceID: svc.ID},
			ports.WithQueue("critical")); err != nil {
			return wrap(err)
		}
	}

	s.notifyService(ctx, svc, "service_renewed", map[string]any{
		"ServiceName": svc.Domain,
		"NextDueDate": next.Format("2006-01-02"),
	})
	s.d.Audit.Log(ctx, 0, "service.renew", "service", svc.ID, nil,
		map[string]any{"next_due_date": next.Format("2006-01-02")})
	return nil
}

// ApplyUpgrade applies the stored pending_upgrade after its diff invoice was
// paid: rebind product/cycle/recurring amount, clear the flag and enqueue the
// panel package change. Tolerates re-runs (no pending upgrade -> nil).
func (s *Service) ApplyUpgrade(ctx context.Context, serviceID int64) error {
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}
	if len(svc.PendingUpgrade) == 0 {
		return nil // already applied - idempotent
	}
	var up domain.ServiceUpgrade
	if err := json.Unmarshal(svc.PendingUpgrade, &up); err != nil {
		return apperr.Internal(fmt.Errorf("provisioning: decode pending_upgrade for service %d: %w", serviceID, err))
	}

	before := map[string]any{
		"product_id": svc.ProductID, "billing_cycle": svc.BillingCycle,
		"recurring_amount": svc.RecurringAmount,
	}
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		svc.ProductID = up.ProductID
		svc.BillingCycle = up.Cycle
		svc.RecurringAmount = up.RecurringAmount
		svc.PendingUpgrade = nil
		// Rebind the chosen-specs snapshot to the new configuration so the
		// enqueued package change rebuilds the panel package from it (and a
		// move to a flat product drops the stale snapshot).
		svc.PanelMeta = panelMetaWithChosenSpecs(svc.PanelMeta, up.Specs)
		return s.d.Services.Update(txCtx, svc)
	})
	if err != nil {
		return wrap(err)
	}

	if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionChangePackage,
		jobs.ProvisionChangePackagePayload{ServiceID: svc.ID},
		ports.WithQueue("critical")); err != nil {
		return wrap(err)
	}

	s.d.Audit.Log(ctx, 0, "service.apply_upgrade", "service", svc.ID, before, map[string]any{
		"product_id": up.ProductID, "billing_cycle": up.Cycle,
		"recurring_amount": up.RecurringAmount, "invoice_id": up.InvoiceID,
	})
	return nil
}

// Automation crons (worker)

// AutoSuspend enqueues provision:suspend for active services whose renewal
// invoice has been overdue longer than automation.suspend_after_days.
// Returns the number of suspensions enqueued.
func (s *Service) AutoSuspend(ctx context.Context) (int, error) {
	days, err := s.d.Settings.GetInt(ctx, settingSuspendAfterDays, defaultSuspendAfterDays)
	if err != nil {
		return 0, wrap(err)
	}
	before := s.today().AddDate(0, 0, -days)

	list, err := s.d.Services.ListOverdueSuspendable(ctx, before)
	if err != nil {
		return 0, wrap(err)
	}

	count := 0
	for _, svc := range list {
		if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionSuspend,
			jobs.ProvisionSuspendPayload{ServiceID: svc.ID, Reason: suspendReasonOverdue},
			ports.WithQueue("critical")); err != nil {
			return count, wrap(err)
		}
		count++
	}
	return count, nil
}

// AutoTerminate enqueues provision:terminate for services suspended longer
// than automation.terminate_after_days and for cancel_at_period_end services
// past their due date. Returns the number of terminations enqueued.
func (s *Service) AutoTerminate(ctx context.Context) (int, error) {
	days, err := s.d.Settings.GetInt(ctx, settingTerminateAfterDays, defaultTerminateAfterDays)
	if err != nil {
		return 0, wrap(err)
	}
	suspendedBefore := s.d.Clock.Now().UTC().AddDate(0, 0, -days)

	list, err := s.d.Services.ListTerminatable(ctx, suspendedBefore, s.today())
	if err != nil {
		return 0, wrap(err)
	}

	count := 0
	for _, svc := range list {
		if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionTerminate,
			jobs.ProvisionTerminatePayload{ServiceID: svc.ID},
			ports.WithQueue("critical")); err != nil {
			return count, wrap(err)
		}
		count++
	}
	return count, nil
}

// Client use-cases

// ListServices lists one client's services (clientID 0 = all, admin), with
// product/server display names attached.
func (s *Service) ListServices(ctx context.Context, clientID int64, p ports.ListParams) ([]ServiceView, int64, error) {
	var (
		list  []domain.Service
		total int64
		err   error
	)
	if clientID == 0 {
		list, total, err = s.d.Services.List(ctx, p)
	} else {
		list, total, err = s.d.Services.ListByClient(ctx, clientID, p)
	}
	if err != nil {
		return nil, 0, wrap(err)
	}
	return s.serviceViews(ctx, list), total, nil
}

// GetService returns one service with ownership enforcement (clientID 0 =
// admin), with product/server display names and its pending cancellation
// request (if any) attached. The bulk ListServices path skips the pending-
// cancellation lookup to avoid an extra query per row.
func (s *Service) GetService(ctx context.Context, clientID, serviceID int64) (*ServiceView, error) {
	svc, err := s.getOwned(ctx, clientID, serviceID)
	if err != nil {
		return nil, err
	}
	views := s.serviceViews(ctx, []domain.Service{*svc})
	view := &views[0]
	pending, err := s.d.CancellationRequests.GetPendingByService(ctx, svc.ID)
	if err != nil {
		return nil, wrap(err)
	}
	view.PendingCancellation = pending
	return view, nil
}

// serviceViews attaches product/server display names to service rows.
// Lookups are memoized per call (a page of rows references few distinct
// products/servers) and best-effort: an unresolvable reference (deleted
// product, missing server) just leaves the name empty - the UI falls back to
// "#<id>" - rather than failing the read.
func (s *Service) serviceViews(ctx context.Context, list []domain.Service) []ServiceView {
	productNames := map[int64]string{}
	type serverInfo struct{ name, hostname string }
	servers := map[int64]serverInfo{}

	views := make([]ServiceView, 0, len(list))
	for _, svc := range list {
		v := ServiceView{Service: svc}
		if _, ok := productNames[svc.ProductID]; !ok {
			name := ""
			if p, err := s.d.Products.GetByID(ctx, svc.ProductID); err == nil && p != nil {
				name = p.Name
			}
			productNames[svc.ProductID] = name
		}
		v.ProductName = productNames[svc.ProductID]
		if svc.ServerID != nil {
			if _, ok := servers[*svc.ServerID]; !ok {
				info := serverInfo{}
				if srv, err := s.d.Servers.GetServerByID(ctx, *svc.ServerID); err == nil && srv != nil {
					info = serverInfo{name: srv.Name, hostname: srv.Hostname}
				}
				servers[*svc.ServerID] = info
			}
			v.ServerName = servers[*svc.ServerID].name
			v.ServerHostname = servers[*svc.ServerID].hostname
		}
		views = append(views, v)
	}
	return views
}

// ChangePassword synchronously changes the control-panel password and stores
// it re-encrypted. clientID 0 = admin (no ownership check).
func (s *Service) ChangePassword(ctx context.Context, actorUserID, clientID, serviceID int64, password string) error {
	return s.changePassword(ctx, actorUserID, clientID, serviceID, password)
}

func (s *Service) changePassword(ctx context.Context, actorUserID, clientID, serviceID int64, password string) error {
	if err := validatePasswordStrength(password); err != nil {
		return err
	}
	svc, err := s.getOwned(ctx, clientID, serviceID)
	if err != nil {
		return err
	}
	if svc.Status != domain.ServiceActive {
		return apperr.Conflict("service must be active to change its password")
	}

	mod, cfg, hasPanel, err := s.panelContext(ctx, svc)
	if err != nil {
		return err
	}
	if !hasPanel {
		return apperr.Conflict("service has no control panel")
	}
	if err := mod.ChangePassword(ctx, cfg, svc.Username, password); err != nil {
		return wrap(err)
	}

	enc, err := s.d.Crypt.Encrypt(password)
	if err != nil {
		return apperr.Internal(fmt.Errorf("provisioning: encrypt service password: %w", err))
	}
	svc.PasswordEnc = enc
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	s.d.Audit.Log(ctx, actorUserID, "service.change_password", "service", svc.ID, nil, nil)
	return nil
}

// validatePasswordStrength enforces the panel password policy: >=12 chars
// with lower, upper and digit.
func validatePasswordStrength(password string) error {
	var details []apperr.FieldError
	if len(password) < 12 {
		details = append(details, apperr.FieldError{Field: "password", Message: "must be at least 12 characters"})
	} else {
		var lower, upper, digit bool
		for _, r := range password {
			switch {
			case r >= 'a' && r <= 'z':
				lower = true
			case r >= 'A' && r <= 'Z':
				upper = true
			case r >= '0' && r <= '9':
				digit = true
			}
		}
		if !lower || !upper || !digit {
			details = append(details, apperr.FieldError{
				Field: "password", Message: "must contain lowercase, uppercase and digit characters",
			})
		}
	}
	if len(details) > 0 {
		return apperr.Validation("weak password", details...)
	}
	return nil
}

// SSO returns a one-time panel login URL for an active service.
func (s *Service) SSO(ctx context.Context, actorUserID, clientID, serviceID int64) (string, error) {
	svc, err := s.getOwned(ctx, clientID, serviceID)
	if err != nil {
		return "", err
	}
	if svc.Status != domain.ServiceActive {
		return "", apperr.Conflict("service must be active for panel login")
	}

	mod, cfg, hasPanel, err := s.panelContext(ctx, svc)
	if err != nil {
		return "", err
	}
	if !hasPanel {
		return "", apperr.Conflict("service has no control panel")
	}
	url, err := mod.SSOURL(ctx, cfg, svc.Username)
	if err != nil {
		return "", wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "service.sso", "service", svc.ID, nil, nil)
	return url, nil
}

// CancelService handles a client cancellation request. immediate: cancel open
// (unpaid/overdue) renewal invoices and enqueue termination right away - no
// admin approval gate - but the request still starts CancellationPending
// (resolved to CancellationAutoProcessed by ProvisionTerminate once the
// worker actually completes it), NOT auto_processed at submission time: the
// termination is asynchronous, so marking it done immediately would let the
// "one pending request per service" guard below miss a second immediate
// submission for as long as the job sits in the queue (the service's own
// status stays active/suspended the whole time too - only ProvisionTerminate
// flips it). end_of_term: create a CancellationPending request and stop
// there - panel_meta.cancel_at_period_end (honoured by AutoTerminate and
// renewal invoice generation) is only set once an admin accepts it via
// AcceptCancellationRequest. A service may have at most one pending request
// at a time.
func (s *Service) CancelService(ctx context.Context, actorUserID, clientID, serviceID int64, in CancelServiceInput) (*domain.Service, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	svc, err := s.getOwned(ctx, clientID, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.Status != domain.ServiceActive && svc.Status != domain.ServiceSuspended &&
		!(svc.Status == domain.ServicePending && in.Mode == CancelModeImmediate) {
		return nil, apperr.Conflict("service cannot be cancelled in its current status")
	}
	pending, err := s.d.CancellationRequests.GetPendingByService(ctx, svc.ID)
	if err != nil {
		return nil, wrap(err)
	}
	if pending != nil {
		return nil, apperr.Conflict("a cancellation request is already pending for this service")
	}

	cr := &domain.CancellationRequest{ServiceID: svc.ID, ClientID: svc.ClientID, Mode: in.Mode, Reason: in.Reason}

	switch in.Mode {
	case CancelModeImmediate:
		if err := s.cancelOpenRenewalInvoices(ctx, svc); err != nil {
			return nil, err
		}
		if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionTerminate,
			jobs.ProvisionTerminatePayload{ServiceID: svc.ID},
			ports.WithQueue("critical")); err != nil {
			return nil, wrap(err)
		}
		cr.Status = domain.CancellationPending
	case CancelModeEndOfTerm:
		cr.Status = domain.CancellationPending
	default:
		return nil, apperr.Validation("invalid cancellation mode",
			apperr.FieldError{Field: "mode", Message: "must be immediate or end_of_term"})
	}

	if err := s.d.CancellationRequests.Create(ctx, cr); err != nil {
		return nil, wrap(err)
	}

	s.d.Audit.Log(ctx, actorUserID, "service.cancel_request", "service", svc.ID, nil,
		map[string]any{"mode": in.Mode, "reason": in.Reason, "request_id": cr.ID})
	return svc, nil
}

// ListCancellationRequests returns cancellation requests (optionally filtered
// by p.Status/ServiceID) with service/client display names attached, for the
// admin cancellation-requests list.
func (s *Service) ListCancellationRequests(ctx context.Context, p ports.ListParams) ([]CancellationRequestView, int64, error) {
	rows, total, err := s.d.CancellationRequests.List(ctx, p)
	if err != nil {
		return nil, 0, wrap(err)
	}
	if len(rows) == 0 {
		return nil, total, nil
	}

	serviceIDs := make([]int64, 0, len(rows))
	seen := map[int64]bool{}
	for _, r := range rows {
		if !seen[r.ServiceID] {
			seen[r.ServiceID] = true
			serviceIDs = append(serviceIDs, r.ServiceID)
		}
	}
	services, err := s.d.Services.GetByIDs(ctx, serviceIDs)
	if err != nil {
		return nil, 0, wrap(err)
	}
	serviceDomains := make(map[int64]string, len(services))
	for _, sv := range services {
		serviceDomains[sv.ID] = sv.Domain
	}

	clientNames := map[int64]string{}
	views := make([]CancellationRequestView, 0, len(rows))
	for _, r := range rows {
		if _, ok := clientNames[r.ClientID]; !ok {
			name := ""
			if c, err := s.d.Clients.GetByID(ctx, r.ClientID); err == nil && c != nil {
				name = c.FullName()
			}
			clientNames[r.ClientID] = name
		}
		views = append(views, CancellationRequestView{
			CancellationRequest: r,
			ServiceDomain:       serviceDomains[r.ServiceID],
			ClientName:          clientNames[r.ClientID],
		})
	}
	return views, total, nil
}

// AcceptCancellationRequest approves a pending end_of_term request: flags
// panel_meta.cancel_at_period_end on the service (honoured by AutoTerminate)
// and marks the request accepted.
func (s *Service) AcceptCancellationRequest(ctx context.Context, actorUserID, requestID int64) error {
	cr, err := s.d.CancellationRequests.GetByID(ctx, requestID)
	if err != nil {
		return wrap(err)
	}
	if cr.Mode != CancelModeEndOfTerm {
		return apperr.Conflict("only end_of_term requests can be accepted/rejected - immediate requests process automatically")
	}
	if _, err := domain.TransitionCancellationRequest(cr.Status, domain.CancellationAccepted); err != nil {
		return err
	}
	svc, err := s.d.Services.GetByID(ctx, cr.ServiceID)
	if err != nil {
		return wrap(err)
	}
	if svc.Status != domain.ServiceActive && svc.Status != domain.ServiceSuspended {
		return apperr.Conflict("service is no longer eligible for cancellation")
	}

	meta := metaMap(svc.PanelMeta)
	meta[metaCancelAtPeriodEnd] = true
	svc.PanelMeta = marshalMeta(meta)
	if err := s.d.Services.Update(ctx, svc); err != nil {
		return wrap(err)
	}

	now := s.d.Clock.Now().UTC()
	cr.Status = domain.CancellationAccepted
	cr.DecidedAt = &now
	cr.DecidedBy = &actorUserID
	if err := s.d.CancellationRequests.Update(ctx, cr); err != nil {
		return wrap(err)
	}

	s.d.Audit.Log(ctx, actorUserID, "service.cancel_request_accept", "service", svc.ID, nil,
		map[string]any{"request_id": cr.ID, "mode": cr.Mode})
	return nil
}

// RejectCancellationRequest denies a pending request; the service is left
// completely untouched.
func (s *Service) RejectCancellationRequest(ctx context.Context, actorUserID, requestID int64) error {
	cr, err := s.d.CancellationRequests.GetByID(ctx, requestID)
	if err != nil {
		return wrap(err)
	}
	if cr.Mode != CancelModeEndOfTerm {
		return apperr.Conflict("only end_of_term requests can be accepted/rejected - immediate requests process automatically")
	}
	if _, err := domain.TransitionCancellationRequest(cr.Status, domain.CancellationRejected); err != nil {
		return err
	}

	now := s.d.Clock.Now().UTC()
	cr.Status = domain.CancellationRejected
	cr.DecidedAt = &now
	cr.DecidedBy = &actorUserID
	if err := s.d.CancellationRequests.Update(ctx, cr); err != nil {
		return wrap(err)
	}

	s.d.Audit.Log(ctx, actorUserID, "service.cancel_request_reject", "service", cr.ServiceID, nil,
		map[string]any{"request_id": cr.ID, "mode": cr.Mode})
	return nil
}

// cancelOpenRenewalInvoices cancels unpaid/overdue invoices that contain a
// service_renewal item for this service.
func (s *Service) cancelOpenRenewalInvoices(ctx context.Context, svc *domain.Service) error {
	for _, status := range []domain.InvoiceStatus{domain.InvoiceUnpaid, domain.InvoiceOverdue} {
		invoices, _, err := s.d.Invoices.ListByClient(ctx, svc.ClientID,
			ports.ListParams{Page: 1, PerPage: 100, Status: string(status)})
		if err != nil {
			return wrap(err)
		}
		if len(invoices) == 0 {
			continue
		}
		ids := make([]int64, len(invoices))
		for i, inv := range invoices {
			ids[i] = inv.ID
		}
		itemsByInvoice, err := s.d.Invoices.GetItemsByInvoiceIDs(ctx, ids)
		if err != nil {
			return wrap(err)
		}
		for _, inv := range invoices {
			for _, item := range itemsByInvoice[inv.ID] {
				if item.RelatedType == domain.RelatedServiceRenewal &&
					item.RelatedID != nil && *item.RelatedID == svc.ID {
					if _, err := domain.TransitionInvoice(inv.Status, domain.InvoiceCancelled); err != nil {
						return err
					}
					if err := s.d.Invoices.UpdateStatus(ctx, inv.ID, domain.InvoiceCancelled, nil); err != nil {
						return wrap(err)
					}
					break
				}
			}
		}
	}
	return nil
}

// UpgradeService starts (or immediately applies) a product/cycle change.
// The prorated difference between the new price and the unused value of the
// current cycle is invoiced (upgrade) or credited (downgrade, floor 0 - never
// a negative invoice).
func (s *Service) UpgradeService(ctx context.Context, actorUserID, clientID, serviceID int64, in UpgradeServiceInput) (*UpgradeResult, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if !in.Cycle.Valid() || domain.CycleMonths(in.Cycle) == 0 {
		return nil, apperr.Validation("invalid billing cycle",
			apperr.FieldError{Field: "cycle", Message: "must be a recurring billing cycle"})
	}

	svc, err := s.getOwned(ctx, clientID, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.Status != domain.ServiceActive {
		return nil, apperr.Conflict("only active services can be upgraded")
	}
	if len(svc.PendingUpgrade) > 0 {
		return nil, apperr.Conflict("an upgrade is already pending payment for this service")
	}

	current, err := s.d.Products.GetByID(ctx, svc.ProductID)
	if err != nil {
		return nil, wrap(err)
	}
	target, err := s.d.Products.GetByID(ctx, in.ProductID)
	if err != nil {
		return nil, wrap(err)
	}
	if target.Module != current.Module {
		return nil, apperr.Validation("target product must use the same server module",
			apperr.FieldError{Field: "product_id", Message: "incompatible module"})
	}

	// Custom-spec (configurable) targets: validate and price the chosen knobs
	// with the same rules as order checkout, so the prorated invoice carries
	// every per-spec charge and ApplyUpgrade can reconfigure the panel package.
	var upSpecs []domain.UpgradeSpec
	var specTotal int64
	if target.Configurable {
		upSpecs, specTotal, err = s.resolveUpgradeSpecs(ctx, target.ID, in.Specs, in.Cycle)
		if err != nil {
			return nil, err
		}
	} else if len(in.Specs) > 0 {
		return nil, apperr.Validation("target product is not configurable",
			apperr.FieldError{Field: "specs", Message: "product does not accept spec selections"})
	}
	if svc.ProductID == in.ProductID && svc.BillingCycle == in.Cycle {
		// Same product+cycle is only meaningful for a configurable product
		// whose specs actually change (a resize); anything else is a no-op.
		if !target.Configurable {
			return nil, apperr.Validation("service already uses this product and cycle")
		}
		if sameChosenSpecs(svc.PanelMeta, upSpecs) {
			return nil, apperr.Validation("service already uses this configuration",
				apperr.FieldError{Field: "specs", Message: "chosen specs match the current configuration"})
		}
	}

	pricing, err := s.d.Products.GetPricing(ctx, target.ID, in.Cycle)
	if err != nil || pricing == nil {
		return nil, apperr.Validation("target product is not priced for this cycle",
			apperr.FieldError{Field: "cycle", Message: "no price configured"})
	}
	targetPrice := pricing.Price + specTotal
	if svc.NextDueDate == nil {
		// Defensive: an active service should always have a next_due_date.
		// Without one there's no cycle end to prorate against, and silently
		// falling back to "now" would zero out both unused and charge,
		// applying the new (possibly pricier) plan for free with no invoice.
		return nil, apperr.Conflict("service has no next_due_date; cannot compute a prorated upgrade")
	}

	now := s.d.Clock.Now().UTC()
	until := *svc.NextDueDate
	unused := domain.Prorate(svc.RecurringAmount, svc.BillingCycle, now, until)
	charge := domain.Prorate(targetPrice, in.Cycle, now, until)
	diff := charge - unused

	if diff > 0 {
		dueDays, err := s.d.Settings.GetInt(ctx, settingInvoiceDueDays, defaultInvoiceDueDays)
		if err != nil {
			return nil, wrap(err)
		}
		desc := fmt.Sprintf("Upgrade %s: %s -> %s (%s)", svc.Domain, current.Name, target.Name, in.Cycle)
		if summary := upgradeSpecSummary(upSpecs); summary != "" {
			desc += " - " + summary
		}
		inv, err := s.d.Billing.CreateInvoice(ctx, ports.CreateInvoiceInput{
			ClientID: svc.ClientID,
			Items: []ports.CreateInvoiceItem{{
				Description: desc,
				Amount:      diff,
				Taxed:       true,
				RelatedType: domain.RelatedServiceUpgrade,
				RelatedID:   svc.ID,
			}},
			DueDate: now.AddDate(0, 0, dueDays),
		})
		if err != nil {
			return nil, wrap(err)
		}

		up := domain.ServiceUpgrade{
			ProductID: target.ID, Cycle: in.Cycle,
			RecurringAmount: targetPrice, InvoiceID: inv.ID,
			Specs: upSpecs,
		}
		raw, _ := json.Marshal(up)
		svc.PendingUpgrade = raw
		if err := s.d.Services.Update(ctx, svc); err != nil {
			return nil, wrap(err)
		}

		s.d.Audit.Log(ctx, actorUserID, "service.upgrade_requested", "service", svc.ID, nil,
			map[string]any{"product_id": target.ID, "cycle": in.Cycle, "diff": diff,
				"invoice_id": inv.ID, "specs": upSpecs})
		return &UpgradeResult{Applied: false, ProratedDiff: diff, Invoice: inv, Service: svc}, nil
	}

	// Downgrade (or zero diff): apply immediately, excess becomes credit.
	credit := -diff
	before := map[string]any{
		"product_id": svc.ProductID, "billing_cycle": svc.BillingCycle,
		"recurring_amount": svc.RecurringAmount,
	}
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		svc.ProductID = target.ID
		svc.BillingCycle = in.Cycle
		svc.RecurringAmount = targetPrice
		svc.PanelMeta = panelMetaWithChosenSpecs(svc.PanelMeta, upSpecs)
		if err := s.d.Services.Update(txCtx, svc); err != nil {
			return err
		}
		if credit > 0 {
			return s.d.Credit.AddCredit(txCtx, svc.ClientID, credit,
				fmt.Sprintf("Downgrade credit for service #%d (%s)", svc.ID, svc.Domain), 0)
		}
		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}

	if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionChangePackage,
		jobs.ProvisionChangePackagePayload{ServiceID: svc.ID},
		ports.WithQueue("critical")); err != nil {
		return nil, wrap(err)
	}

	s.d.Audit.Log(ctx, actorUserID, "service.downgrade_applied", "service", svc.ID, before,
		map[string]any{"product_id": target.ID, "cycle": in.Cycle, "credit": credit, "specs": upSpecs})
	return &UpgradeResult{Applied: true, ProratedDiff: diff, CreditIssued: credit, Service: svc}, nil
}

// resolveUpgradeSpecs validates and prices the chosen dynamic specs of a
// configurable upgrade target with the same rules as order checkout
// (orders.planProduct/priceSpec): every defined knob is resolved (defaults for
// the ones not chosen), quantities honor min/max/step and the unlimited flag,
// and each knob adds qty-above-included times the cycle's unit price - or the
// flat unlimited add-on - to the recurring amount.
func (s *Service) resolveUpgradeSpecs(ctx context.Context, productID int64, chosen []UpgradeSpecInput, cycle domain.BillingCycle) ([]domain.UpgradeSpec, int64, error) {
	defs, err := s.d.Products.ListSpecs(ctx, productID)
	if err != nil {
		return nil, 0, wrap(err)
	}
	byKey := make(map[string]UpgradeSpecInput, len(chosen))
	for _, c := range chosen {
		byKey[c.Key] = c
	}

	var (
		out   []domain.UpgradeSpec
		total int64
		errs  []apperr.FieldError
	)
	valid := make(map[string]bool, len(defs))
	for _, sp := range defs {
		valid[sp.Key] = true
		qty, unlimited := sp.DefaultQty, false
		if c, ok := byKey[sp.Key]; ok {
			qty, unlimited = c.Qty, c.Unlimited
		}
		fe := func(msg string) {
			errs = append(errs, apperr.FieldError{Field: "specs." + sp.Key, Message: msg})
		}
		if unlimited {
			if !sp.AllowUnlimited {
				fe("unlimited is not allowed for this spec")
				continue
			}
		} else {
			bad := false
			if qty < sp.MinQty {
				fe(fmt.Sprintf("must be at least %d", sp.MinQty))
				bad = true
			}
			if sp.MaxQty != 0 && qty > sp.MaxQty {
				fe(fmt.Sprintf("must be at most %d", sp.MaxQty))
				bad = true
			}
			if sp.StepQty > 1 && (qty-sp.MinQty)%sp.StepQty != 0 {
				fe(fmt.Sprintf("must be in increments of %d", sp.StepQty))
				bad = true
			}
			if bad {
				continue
			}
		}

		pricing, err := s.d.Products.GetSpecPricing(ctx, sp.ID, cycle)
		if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
			return nil, 0, wrap(err)
		}
		us := domain.UpgradeSpec{
			Key: sp.Key, ProvisionKey: string(sp.ProvisionKey), Unit: string(sp.Unit),
			Qty: qty, Unlimited: unlimited,
		}
		if unlimited {
			us.Qty = domain.UnlimitedQty
			if pricing != nil {
				us.Amount = pricing.UnlimitedPrice
			}
		} else if pricing != nil {
			chargeable := qty - sp.IncludedQty
			if chargeable < 0 {
				chargeable = 0
			}
			us.Amount = chargeable * pricing.UnitPrice
		}
		total += us.Amount
		out = append(out, us)
	}
	for k := range byKey {
		if !valid[k] {
			errs = append(errs, apperr.FieldError{Field: "specs." + k, Message: "unknown spec"})
		}
	}
	if len(errs) > 0 {
		return nil, 0, apperr.Validation("invalid spec selections", errs...)
	}
	return out, total, nil
}

// sameChosenSpecs reports whether the resolved upgrade specs match the
// service's current chosen_specs snapshot knob-for-knob (key, qty, unlimited).
// A service without a snapshot never matches, so a first-time spec selection
// on the same product always goes through.
func sameChosenSpecs(panelMeta json.RawMessage, specs []domain.UpgradeSpec) bool {
	raw, ok := metaMap(panelMeta)[domain.PanelMetaChosenSpecs]
	if !ok {
		return false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var cur []chosenSpec
	if err := json.Unmarshal(b, &cur); err != nil {
		return false
	}
	if len(cur) != len(specs) {
		return false
	}
	type choice struct {
		qty       int64
		unlimited bool
	}
	m := make(map[string]choice, len(cur))
	for _, c := range cur {
		m[c.Key] = choice{qty: c.Qty, unlimited: c.Unlimited}
	}
	for _, sp := range specs {
		c, ok := m[sp.Key]
		if !ok || c.qty != sp.Qty || c.unlimited != sp.Unlimited {
			return false
		}
	}
	return true
}

// panelMetaWithChosenSpecs returns panel_meta with the chosen_specs snapshot replaced
// (or removed when the new configuration has no specs - e.g. moving to a
// flat, non-configurable product).
func panelMetaWithChosenSpecs(panelMeta json.RawMessage, specs []domain.UpgradeSpec) json.RawMessage {
	meta := metaMap(panelMeta)
	if len(specs) > 0 {
		meta[domain.PanelMetaChosenSpecs] = specs
	} else {
		delete(meta, domain.PanelMetaChosenSpecs)
	}
	return marshalMeta(meta)
}

// upgradeSpecSummary renders a human-readable spec list for the upgrade
// invoice line (same format as the order checkout's specSummary).
func upgradeSpecSummary(specs []domain.UpgradeSpec) string {
	if len(specs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(specs))
	for _, sp := range specs {
		qty := "unlimited"
		if !sp.Unlimited {
			unit := ""
			switch domain.SpecUnit(sp.Unit) {
			case domain.UnitGB:
				unit = "GB"
			case domain.UnitMB:
				unit = "MB"
			}
			qty = fmt.Sprintf("%d%s", sp.Qty, unit)
		}
		parts = append(parts, fmt.Sprintf("%s %s", qty, sp.Key))
	}
	return strings.Join(parts, ", ")
}

// Admin: service actions

// AdminAction runs a provisioning action for an admin: synchronously through
// the same lifecycle methods, or enqueued when async is true.
func (s *Service) AdminAction(ctx context.Context, actorUserID, serviceID int64, action, reason string, async bool) error {
	// Existence check up-front for fast feedback on the enqueue path.
	if _, err := s.d.Services.GetByID(ctx, serviceID); err != nil {
		return wrap(err)
	}

	if async {
		var (
			taskType string
			payload  any
		)
		switch action {
		case ActionCreate:
			taskType, payload = jobs.TypeProvisionCreate, jobs.ProvisionCreatePayload{ServiceID: serviceID}
		case ActionSuspend:
			taskType, payload = jobs.TypeProvisionSuspend, jobs.ProvisionSuspendPayload{ServiceID: serviceID, Reason: reason}
		case ActionUnsuspend:
			taskType, payload = jobs.TypeProvisionUnsuspend, jobs.ProvisionUnsuspendPayload{ServiceID: serviceID}
		case ActionTerminate:
			taskType, payload = jobs.TypeProvisionTerminate, jobs.ProvisionTerminatePayload{ServiceID: serviceID}
		default:
			return apperr.Validation("unknown service action")
		}
		if err := s.d.Queue.Enqueue(ctx, taskType, payload, ports.WithQueue("critical")); err != nil {
			return wrap(err)
		}
		s.d.Audit.Log(ctx, actorUserID, "service.enqueue_"+action, "service", serviceID, nil,
			map[string]any{"reason": reason})
		return nil
	}

	var err error
	switch action {
	case ActionCreate:
		err = s.ProvisionCreate(ctx, serviceID)
	case ActionSuspend:
		err = s.ProvisionSuspend(ctx, serviceID, reason)
	case ActionUnsuspend:
		err = s.ProvisionUnsuspend(ctx, serviceID)
	case ActionTerminate:
		err = s.ProvisionTerminate(ctx, serviceID)
	default:
		return apperr.Validation("unknown service action")
	}
	if err != nil {
		return err
	}
	s.d.Audit.Log(ctx, actorUserID, "service.admin_"+action, "service", serviceID, nil,
		map[string]any{"reason": reason})
	return nil
}

// AdminChangePackage optionally re-binds the service to another product of
// the same module, then pushes the package to the panel (sync or enqueued).
func (s *Service) AdminChangePackage(ctx context.Context, actorUserID, serviceID int64, in AdminChangePackageInput) error {
	if err := s.val.Struct(in); err != nil {
		return err
	}
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return wrap(err)
	}

	if in.ProductID != nil && *in.ProductID != svc.ProductID {
		current, err := s.d.Products.GetByID(ctx, svc.ProductID)
		if err != nil {
			return wrap(err)
		}
		target, err := s.d.Products.GetByID(ctx, *in.ProductID)
		if err != nil {
			return wrap(err)
		}
		if target.Module != current.Module {
			return apperr.Validation("target product must use the same server module",
				apperr.FieldError{Field: "product_id", Message: "incompatible module"})
		}
		before := map[string]any{"product_id": svc.ProductID}
		svc.ProductID = target.ID
		if err := s.d.Services.Update(ctx, svc); err != nil {
			return wrap(err)
		}
		s.d.Audit.Log(ctx, actorUserID, "service.rebind_product", "service", svc.ID, before,
			map[string]any{"product_id": target.ID})
	}

	if in.Async {
		if err := s.d.Queue.Enqueue(ctx, jobs.TypeProvisionChangePackage,
			jobs.ProvisionChangePackagePayload{ServiceID: serviceID},
			ports.WithQueue("critical")); err != nil {
			return wrap(err)
		}
		s.d.Audit.Log(ctx, actorUserID, "service.enqueue_change_package", "service", serviceID, nil, nil)
		return nil
	}
	return s.ProvisionChangePackage(ctx, serviceID)
}

// AdminUpdateService patches a service's plain-field record - domain,
// username, server, billing cycle, recurring amount, dates, suspend reason,
// and/or notes. Product, password, and status changes go through their own
// dedicated endpoints instead (see AdminUpdateServiceInput's doc comment).
func (s *Service) AdminUpdateService(ctx context.Context, actorUserID, serviceID int64, in AdminUpdateServiceInput) (*domain.Service, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if in.Domain == nil && in.Username == nil && in.ServerID == nil && in.BillingCycle == nil &&
		in.RecurringAmount == nil && in.NextDueDate == nil && in.RegistrationDate == nil &&
		in.TerminatedAt == nil && in.SuspendReason == nil && in.Notes == nil {
		return nil, apperr.Validation("nothing to update")
	}
	svc, err := s.d.Services.GetByID(ctx, serviceID)
	if err != nil {
		return nil, wrap(err)
	}

	before := map[string]any{}
	after := map[string]any{}
	if in.ServerID != nil {
		if _, err := s.d.Servers.GetServerByID(ctx, *in.ServerID); err != nil {
			return nil, wrap(err)
		}
		before["server_id"] = svc.ServerID
		svc.ServerID = in.ServerID
		after["server_id"] = *in.ServerID
	}
	if in.Domain != nil {
		before["domain"] = svc.Domain
		svc.Domain = *in.Domain
		after["domain"] = *in.Domain
	}
	if in.Username != nil {
		before["username"] = svc.Username
		svc.Username = *in.Username
		after["username"] = *in.Username
	}
	if in.BillingCycle != nil {
		before["billing_cycle"] = svc.BillingCycle
		svc.BillingCycle = domain.BillingCycle(*in.BillingCycle)
		after["billing_cycle"] = *in.BillingCycle
	}
	if in.RecurringAmount != nil {
		before["recurring_amount"] = svc.RecurringAmount
		svc.RecurringAmount = *in.RecurringAmount
		after["recurring_amount"] = *in.RecurringAmount
	}
	if in.NextDueDate != nil {
		due, perr := time.Parse("2006-01-02", *in.NextDueDate)
		if perr != nil {
			return nil, apperr.Validation("invalid next_due_date",
				apperr.FieldError{Field: "next_due_date", Message: "must be YYYY-MM-DD"})
		}
		before["next_due_date"] = svc.NextDueDate
		svc.NextDueDate = &due
		after["next_due_date"] = due
	}
	if in.RegistrationDate != nil {
		reg, perr := time.Parse("2006-01-02", *in.RegistrationDate)
		if perr != nil {
			return nil, apperr.Validation("invalid registration_date",
				apperr.FieldError{Field: "registration_date", Message: "must be YYYY-MM-DD"})
		}
		before["registration_date"] = svc.RegistrationDate
		svc.RegistrationDate = &reg
		after["registration_date"] = reg
	}
	if in.TerminatedAt != nil {
		term, perr := time.Parse("2006-01-02", *in.TerminatedAt)
		if perr != nil {
			return nil, apperr.Validation("invalid terminated_at",
				apperr.FieldError{Field: "terminated_at", Message: "must be YYYY-MM-DD"})
		}
		termUTC := term.UTC()
		before["terminated_at"] = svc.TerminatedAt
		svc.TerminatedAt = &termUTC
		after["terminated_at"] = termUTC
	}
	if in.SuspendReason != nil {
		before["suspend_reason"] = svc.SuspendReason
		svc.SuspendReason = *in.SuspendReason
		after["suspend_reason"] = *in.SuspendReason
	}
	if in.Notes != nil {
		before["notes"] = svc.Notes
		svc.Notes = *in.Notes
		after["notes"] = *in.Notes
	}

	if err := s.d.Services.Update(ctx, svc); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "service.update", "service", svc.ID, before, after)
	return svc, nil
}

// Admin: servers

// ListServers lists servers (secrets never serialized).
func (s *Service) ListServers(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
	list, total, err := s.d.Servers.ListServers(ctx, p)
	if err != nil {
		return nil, 0, wrap(err)
	}
	return list, total, nil
}

// GetServer returns one server.
func (s *Service) GetServer(ctx context.Context, id int64) (*domain.Server, error) {
	server, err := s.d.Servers.GetServerByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	return server, nil
}

// CreateServer creates a server, encrypting the provided secrets.
func (s *Service) CreateServer(ctx context.Context, actorUserID int64, in ServerInput) (*domain.Server, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if in.GroupID != nil {
		if _, err := s.d.Servers.GetGroupByID(ctx, *in.GroupID); err != nil {
			return nil, wrap(err)
		}
	}

	server := &domain.Server{
		GroupID:       in.GroupID,
		Name:          in.Name,
		Module:        domain.ServerModuleName(in.Module),
		Hostname:      in.Hostname,
		Port:          in.Port,
		Username:      in.Username,
		UseSSL:        true,
		Nameserver1:   in.Nameserver1,
		Nameserver2:   in.Nameserver2,
		Nameserver3:   in.Nameserver3,
		Nameserver4:   in.Nameserver4,
		MaxAccounts:   in.MaxAccounts,
		PackagePrefix: in.PackagePrefix,
		IPAddress:     in.IPAddress,
		Active:        true,
	}
	if in.UseSSL != nil {
		server.UseSSL = *in.UseSSL
	}
	if in.Active != nil {
		server.Active = *in.Active
	}
	if server.Port == 0 {
		server.Port = defaultPort(server.Module)
	}
	if err := s.encryptServerSecrets(server, in.Password, in.APIToken); err != nil {
		return nil, err
	}

	if err := s.d.Servers.CreateServer(ctx, server); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "server.create", "server", server.ID, nil,
		map[string]any{"name": server.Name, "module": server.Module, "hostname": server.Hostname})
	return server, nil
}

// UpdateServer updates a server; empty secret fields keep the stored values.
func (s *Service) UpdateServer(ctx context.Context, actorUserID, id int64, in ServerInput) (*domain.Server, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	server, err := s.d.Servers.GetServerByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if in.GroupID != nil {
		if _, err := s.d.Servers.GetGroupByID(ctx, *in.GroupID); err != nil {
			return nil, wrap(err)
		}
	}

	before := map[string]any{"name": server.Name, "hostname": server.Hostname,
		"module": server.Module, "active": server.Active}

	server.GroupID = in.GroupID
	server.Name = in.Name
	server.Module = domain.ServerModuleName(in.Module)
	server.Hostname = in.Hostname
	server.Username = in.Username
	server.Nameserver1 = in.Nameserver1
	server.Nameserver2 = in.Nameserver2
	server.Nameserver3 = in.Nameserver3
	server.Nameserver4 = in.Nameserver4
	server.MaxAccounts = in.MaxAccounts
	server.PackagePrefix = in.PackagePrefix
	server.IPAddress = in.IPAddress
	if in.Port != 0 {
		server.Port = in.Port
	}
	if in.UseSSL != nil {
		server.UseSSL = *in.UseSSL
	}
	if in.Active != nil {
		server.Active = *in.Active
	}
	if err := s.encryptServerSecrets(server, in.Password, in.APIToken); err != nil {
		return nil, err
	}

	if err := s.d.Servers.UpdateServer(ctx, server); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "server.update", "server", server.ID, before,
		map[string]any{"name": server.Name, "hostname": server.Hostname,
			"module": server.Module, "active": server.Active})
	return server, nil
}

// encryptServerSecrets encrypts non-empty plaintext secrets onto the row.
func (s *Service) encryptServerSecrets(server *domain.Server, password, apiToken string) error {
	if password != "" {
		enc, err := s.d.Crypt.Encrypt(password)
		if err != nil {
			return apperr.Internal(fmt.Errorf("provisioning: encrypt server password: %w", err))
		}
		server.PasswordEnc = enc
	}
	if apiToken != "" {
		enc, err := s.d.Crypt.Encrypt(apiToken)
		if err != nil {
			return apperr.Internal(fmt.Errorf("provisioning: encrypt server api token: %w", err))
		}
		server.APITokenEnc = enc
	}
	return nil
}

func defaultPort(module domain.ServerModuleName) int {
	switch module {
	case domain.ModuleCpanel:
		return 2087
	case domain.ModuleDirectAdmin:
		return 2222
	default:
		return 0
	}
}

// DeleteServer removes a server that hosts no live accounts.
func (s *Service) DeleteServer(ctx context.Context, actorUserID, id int64) error {
	server, err := s.d.Servers.GetServerByID(ctx, id)
	if err != nil {
		return wrap(err)
	}
	n, err := s.d.Services.CountByServer(ctx, id)
	if err != nil {
		return wrap(err)
	}
	if n > 0 {
		return apperr.Conflict("server still hosts services")
	}
	if err := s.d.Servers.DeleteServer(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "server.delete", "server", id, map[string]any{"name": server.Name}, nil)
	return nil
}

// TestConnection probes a server with a read-only connectivity check (WHM
// `version`; DirectAdmin user listing) - it does NOT require an existing
// hosting account, so a fresh server no longer fails with "cpanel account not
// found". It drives either an unsaved form (in.ID == 0) or a stored server
// (in.ID > 0, blank secrets filled from the encrypted row). Connection failures
// are reported in the result (OK=false), not as an HTTP error; only truly
// invalid input / internal faults return an error.
func (s *Service) TestConnection(ctx context.Context, in TestConnectionInput) (*TestConnectionResult, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	var stored *domain.Server
	if in.ID > 0 {
		sv, err := s.d.Servers.GetServerByID(ctx, in.ID)
		if err != nil {
			return nil, wrap(err)
		}
		stored = sv
	}
	cfg, err := s.mergeTestConfig(in, stored)
	if err != nil {
		return nil, err
	}
	if cfg.Hostname == "" {
		return &TestConnectionResult{OK: false, Message: "hostname is required"}, nil
	}
	mod, err := s.moduleFor(cfg.Module)
	if err != nil {
		return nil, err
	}
	if mod == nil {
		return &TestConnectionResult{OK: false, Message: "server has no provisioning module"}, nil
	}

	info, err := mod.TestConnection(ctx, cfg)
	if err != nil {
		return &TestConnectionResult{OK: false, Message: connErrMessage(err)}, nil
	}
	return newTestConnectionResult(info), nil
}

// mergeTestConfig builds the effective ports.ServerConfig for a test probe:
// the stored server (decrypted) is the base when editing, and any non-empty
// input field overlays it. A pre-save test (stored == nil) defaults UseSSL to
// true and derives the port from the module when unset.
func (s *Service) mergeTestConfig(in TestConnectionInput, stored *domain.Server) (ports.ServerConfig, error) {
	var cfg ports.ServerConfig
	if stored != nil {
		c, err := s.serverConfig(stored)
		if err != nil {
			return cfg, err
		}
		cfg = c
	}
	if in.Module != "" {
		cfg.Module = domain.ServerModuleName(in.Module)
	}
	if in.Hostname != "" {
		cfg.Hostname = in.Hostname
	}
	if in.Port != 0 {
		cfg.Port = in.Port
	}
	if in.Username != "" {
		cfg.Username = in.Username
	}
	if in.Password != "" {
		cfg.Password = in.Password
	}
	if in.APIToken != "" {
		cfg.APIToken = in.APIToken
	}
	switch {
	case in.UseSSL != nil:
		cfg.UseSSL = *in.UseSSL
	case stored == nil:
		cfg.UseSSL = true
	}
	if cfg.Port == 0 {
		cfg.Port = defaultPort(cfg.Module)
	}
	return cfg, nil
}

// newTestConnectionResult maps read-only probe metadata into the API result,
// folding the panel version into the success message when reported.
func newTestConnectionResult(info *ports.ServerInfo) *TestConnectionResult {
	r := &TestConnectionResult{OK: true, Message: "connection successful"}
	if info != nil {
		r.Version = info.Version
		r.Hostname = info.Hostname
		r.Nameservers = info.Nameservers
		if info.Version != "" {
			r.Message = "connection successful (" + info.Version + ")"
		}
	}
	return r
}

// connErrMessage renders an adapter error for the OK=false result - the wrapped
// cause (the real upstream reason) when present, else the apperr message.
func connErrMessage(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		if cause := errors.Unwrap(ae); cause != nil {
			return cause.Error()
		}
		return ae.Message
	}
	return err.Error()
}

// Admin: server groups

// ListGroups returns all server groups.
func (s *Service) ListGroups(ctx context.Context) ([]domain.ServerGroup, error) {
	groups, err := s.d.Servers.ListGroups(ctx)
	if err != nil {
		return nil, wrap(err)
	}
	return groups, nil
}

// GetGroup returns one server group.
func (s *Service) GetGroup(ctx context.Context, id int64) (*domain.ServerGroup, error) {
	g, err := s.d.Servers.GetGroupByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	return g, nil
}

// CreateGroup creates a server group.
func (s *Service) CreateGroup(ctx context.Context, actorUserID int64, in ServerGroupInput) (*domain.ServerGroup, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	g := &domain.ServerGroup{Name: in.Name, Strategy: domain.ServerGroupStrategy(in.Strategy)}
	if err := s.d.Servers.CreateGroup(ctx, g); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "server_group.create", "server_group", g.ID, nil,
		map[string]any{"name": g.Name, "strategy": g.Strategy})
	return g, nil
}

// UpdateGroup updates a server group.
func (s *Service) UpdateGroup(ctx context.Context, actorUserID, id int64, in ServerGroupInput) (*domain.ServerGroup, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	g, err := s.d.Servers.GetGroupByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	before := map[string]any{"name": g.Name, "strategy": g.Strategy}
	g.Name = in.Name
	g.Strategy = domain.ServerGroupStrategy(in.Strategy)
	if err := s.d.Servers.UpdateGroup(ctx, g); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "server_group.update", "server_group", g.ID, before,
		map[string]any{"name": g.Name, "strategy": g.Strategy})
	return g, nil
}

// DeleteGroup removes an empty server group.
func (s *Service) DeleteGroup(ctx context.Context, actorUserID, id int64) error {
	if _, err := s.d.Servers.GetGroupByID(ctx, id); err != nil {
		return wrap(err)
	}
	if err := s.d.Servers.DeleteGroup(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "server_group.delete", "server_group", id, nil, nil)
	return nil
}

// ListPackages previews the hosting packages/plans currently defined on a
// representative server in the group (the same PickServer used at
// provisioning time), so the admin product form can pick an existing name
// instead of free-typing one. An invalid group or one with no available
// server is a hard error; a panel probe failure (bad credentials,
// unreachable host) folds into OK=false so the UI can fall back to manual
// entry instead of showing a form error.
func (s *Service) ListPackages(ctx context.Context, groupID int64) (*PackageListResult, error) {
	server, err := s.d.Servers.PickServer(ctx, groupID)
	if err != nil {
		return nil, wrap(err)
	}
	cfg, err := s.serverConfig(server)
	if err != nil {
		return nil, err
	}
	mod, err := s.moduleFor(cfg.Module)
	if err != nil {
		return nil, err
	}
	if mod == nil {
		return &PackageListResult{OK: false, Message: "server has no provisioning module"}, nil
	}
	names, err := mod.ListPackages(ctx, cfg)
	if err != nil {
		return &PackageListResult{OK: false, Message: connErrMessage(err)}, nil
	}
	return &PackageListResult{OK: true, Packages: names}, nil
}
