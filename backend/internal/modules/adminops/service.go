// Package adminops implements the M-ADMINOPS module: admin dashboard KPIs,
// revenue/orders/services reports (JSON + CSV), staff management, log
// listings, gateway configuration and log housekeeping.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	GET   /admin/dashboard              dashboard KPIs + recent activity/orders/tickets (cached 60s; ?refresh=1 bypasses cache)
//	GET   /admin/reports/revenue        ?from&to&group_by=day|month&format=csv   [perm: reports]
//	GET   /admin/reports/orders         ?from&to&format=csv                      [perm: reports]
//	GET   /admin/reports/services       ?format=csv                              [perm: reports]
//	GET   /admin/staff                  ?search&status&page&per_page             [role: admin]
//	POST  /admin/staff                                                            [role: admin]
//	GET   /admin/staff/:id                                                        [role: admin]
//	PATCH /admin/staff/:id                                                        [role: admin]
//	POST  /admin/staff/:id/deactivate                                             [role: admin]
//	GET   /admin/logs/audit             ?user_id&entity&action&from&to           [perm: logs]
//	GET   /admin/logs/email             ?search&status                           [perm: logs]
//	GET   /admin/logs/integration       ?provider&success                        [perm: logs]
//	GET   /admin/logs/queue             ?type&state&page&per_page (Pending Module Actions) [perm: logs]
//	POST  /admin/logs/queue/:queue/:id/retry   re-run one module action now      [perm: logs]
//	DELETE /admin/logs/queue/:queue/:id        dismiss one module action         [perm: logs]
//	DELETE /admin/logs/queue            ?type&state - dismiss every matching action [perm: logs]
//	GET   /admin/gateways                                                         [role: admin]
//	PUT   /admin/gateways                                                         [role: admin]
//
// Housekeep(ctx) is the cron:housekeeping entrypoint consumed by the worker.
package adminops

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// KnownPermissionModules whitelists the staff permissions JSONB keys
// (per-module flags checked by transport RequirePermission).
var KnownPermissionModules = map[string]bool{
	"clients":       true,
	"orders":        true,
	"billing":       true,
	"payments":      true,
	"services":      true,
	"domains":       true,
	"support":       true,
	"products":      true,
	"servers":       true,
	"registrars":    true,
	"settings":      true,
	"reports":       true,
	"logs":          true,
	"staff":         true,
	"gateways":      true,
	"announcements": true,
	"knowledgebase": true,
	"network":       true,
}

// Dashboard cache parameters.
const (
	dashboardCacheKey = "adminops:dashboard"
	dashboardCacheTTL = 60 * time.Second
)

// Housekeeping retention windows (MODULES.md §3 M-ADMINOPS).
const (
	logRetentionDays   = 90  // integration_logs + email_log
	auditRetentionDays = 365 // audit_logs
)

// Settings keys for the duitku gateway configuration. Exported:
// internal/composition/build.go reads all of these to resolve live
// credentials (merchant code, mode/base URL, API key) for the running
// *duitku.Client without a process restart.
const (
	SettingDuitkuMerchantCode = "gateway.duitku.merchant_code"
	SettingDuitkuMode         = "gateway.duitku.mode"

	// SettingDuitkuBaseURL holds an optional, non-secret "custom endpoint"
	// override (e.g. to point at the mockserver instead of a mode-derived
	// sandbox.duitku.com/passport.duitku.com URL) - blank means "derive from
	// Mode", same idea as registrars.base_url (CONTRACTS.md §5/§10).
	SettingDuitkuBaseURL = "gateway.duitku.base_url"

	// SettingDuitkuAPIKeyEnc holds the AES-256-GCM-encrypted Duitku API key
	// (ports.Encryptor) - an admin-configurable dynamic credential, env var
	// as fallback when blank.
	SettingDuitkuAPIKeyEnc = "gateway.duitku.api_key_enc"
)

// SettingManualGatewayConfig holds the manual bank-transfer gateway config
// as one JSON blob ({enabled, accounts, instructions}) - non-secret (bank
// account numbers are display instructions, not credentials), admin-only
// with no env fallback (there's no reasonable env-var shape for a list of
// bank accounts). Read live by internal/integration/manual's adapter.
const SettingManualGatewayConfig = "gateway.manual.config"

// DashboardStore runs the module's aggregate SQL (implemented by *Repo,
// which also satisfies ports.DashboardRepo).
type DashboardStore interface {
	Stats(ctx context.Context) (*ports.DashboardStats, error)
	Revenue(ctx context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error)
	OrdersReport(ctx context.Context, from, to time.Time) ([]ports.RevenuePoint, error)
	ServicesReport(ctx context.Context) (map[string]int64, error)
	ExtraStats(ctx context.Context) (*ExtraStats, error)
	RevenueByGateway(ctx context.Context, from, to time.Time) ([]GatewayRevenue, error)
	ServicesByProduct(ctx context.Context) ([]ProductStatusCount, error)
	RecentOrders(ctx context.Context, limit int) ([]RecentOrderRow, error)
	RecentTickets(ctx context.Context, limit int) ([]RecentTicketRow, error)
}

// LogStore lists and purges log rows with filters the generic ports repo
// interfaces cannot express (implemented by *Repo).
type LogStore interface {
	ListAudit(ctx context.Context, f AuditLogFilter) ([]domain.AuditLog, int64, error)
	ListIntegration(ctx context.Context, f IntegrationLogFilter) ([]domain.IntegrationLog, int64, error)
	PurgeIntegrationLogsBefore(ctx context.Context, before time.Time) (int64, error)
	PurgeEmailLogBefore(ctx context.Context, before time.Time) (int64, error)
	PurgeAuditLogsBefore(ctx context.Context, before time.Time) (int64, error)
}

// StaffLister lists users with role=staff (implemented by *Repo;
// ports.UserRepo.List cannot filter by role).
type StaffLister interface {
	ListStaff(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error)
}

// Deps are the service dependencies (small interfaces, wired by the
// composition root).
type Deps struct {
	Dashboard DashboardStore
	Logs      LogStore
	Staff     StaffLister
	Users     ports.UserRepo     // staff CRUD (impl provided by the auth module)
	Audit     ports.AuditRepo    // dashboard recent activity
	EmailLogs ports.EmailLogRepo // email log listing
	Settings  ports.SettingsRepo // gateway config
	Cache     ports.Cache        // dashboard cache (60s)
	Hasher    ports.PasswordHasher
	AuditLog  ports.AuditLogger
	Clock     ports.Clock
	Presence  ports.PresenceTracker // "Staff Online" widget
	Secrets   GatewaySecrets
	Encryptor ports.Encryptor    // encrypts the admin-configurable Duitku API key at rest
	Jobs      ports.JobInspector // Pending Module Actions queue
}

// Service implements the adminops use-cases.
type Service struct {
	d   Deps
	val *validate.Validator
}

// New builds the adminops Service.
func New(d Deps) *Service {
	return &Service{d: d, val: validate.New()}
}

// Dashboard

// Dashboard returns the admin dashboard KPIs plus recent activity, cached
// for 60 seconds. Cache failures are non-fatal (fail open to the DB). When
// force is true, the cache read is skipped and the KPIs are recomputed from
// the database (e.g. an admin explicitly clicking "Refresh") - the freshly
// computed result still repopulates the cache afterward, so the next
// ordinary (non-forced) read within the TTL benefits from it too.
func (s *Service) Dashboard(ctx context.Context, force bool) (*DashboardData, error) {
	if !force {
		var cached DashboardData
		if hit, err := s.d.Cache.GetJSON(ctx, dashboardCacheKey, &cached); err == nil && hit {
			return &cached, nil
		}
	}

	stats, err := s.d.Dashboard.Stats(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if stats == nil {
		stats = &ports.DashboardStats{}
	}
	extra, err := s.d.Dashboard.ExtraStats(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if extra == nil {
		extra = &ExtraStats{}
	}
	recent, _, err := s.d.Audit.List(ctx, ports.ListParams{Page: 1, PerPage: 10})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	recentOrders, err := s.d.Dashboard.RecentOrders(ctx, recentRowsLimit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	recentTickets, err := s.d.Dashboard.RecentTickets(ctx, recentRowsLimit)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	data := &DashboardData{
		Stats:          *stats,
		Extra:          *extra,
		RecentActivity: recent,
		RecentOrders:   recentOrders,
		RecentTickets:  recentTickets,
		GeneratedAt:    s.d.Clock.Now().UTC(),
	}
	if s.d.Jobs != nil {
		if _, total, err := s.d.Jobs.ListModuleActions(ctx, ports.ModuleActionFilter{Page: 1, PerPage: 1}); err == nil {
			data.ModuleActionsPending = total
		}
	}
	_ = s.d.Cache.SetJSON(ctx, dashboardCacheKey, data, dashboardCacheTTL) // best effort
	return data, nil
}

// Reports

// normalizeRange applies defaults (last 30 days) and returns the inclusive
// display dates plus the [start, end) query bounds.
func (s *Service) normalizeRange(from, to time.Time) (start, end time.Time, err error) {
	now := s.d.Clock.Now().UTC()
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = to.AddDate(0, 0, -30)
	}
	start = truncateDate(from)
	if start.After(truncateDate(to)) {
		return time.Time{}, time.Time{}, apperr.Validation("'from' must not be after 'to'")
	}
	end = truncateDate(to).AddDate(0, 0, 1) // inclusive "to" date
	return start, end, nil
}

func truncateDate(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

const dateFormat = "2006-01-02"

// RevenueReport returns paid-transaction revenue grouped by day or month
// between from and to (inclusive dates; zero values default to the last 30
// days), with totals and a per-gateway split.
func (s *Service) RevenueReport(ctx context.Context, from, to time.Time, groupBy string) (*RevenueReportData, error) {
	if groupBy == "" {
		groupBy = "day"
	}
	if groupBy != "day" && groupBy != "month" {
		return nil, apperr.Validation("group_by must be one of: day, month")
	}
	start, end, err := s.normalizeRange(from, to)
	if err != nil {
		return nil, err
	}

	points, err := s.d.Dashboard.Revenue(ctx, start, end, groupBy)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	byGateway, err := s.d.Dashboard.RevenueByGateway(ctx, start, end)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	data := &RevenueReportData{
		From:      start.Format(dateFormat),
		To:        end.AddDate(0, 0, -1).Format(dateFormat),
		GroupBy:   groupBy,
		Points:    points,
		ByGateway: byGateway,
	}
	for _, p := range points {
		data.TotalAmount += p.Amount
		data.TotalCount += p.Count
	}
	return data, nil
}

// OrdersReport returns orders grouped per day between from and to
// (inclusive dates; zero values default to the last 30 days).
func (s *Service) OrdersReport(ctx context.Context, from, to time.Time) (*OrdersReportData, error) {
	start, end, err := s.normalizeRange(from, to)
	if err != nil {
		return nil, err
	}
	points, err := s.d.Dashboard.OrdersReport(ctx, start, end)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	data := &OrdersReportData{
		From:   start.Format(dateFormat),
		To:     end.AddDate(0, 0, -1).Format(dateFormat),
		Points: points,
	}
	for _, p := range points {
		data.TotalAmount += p.Amount
		data.TotalCount += p.Count
	}
	return data, nil
}

// ServicesReport returns service counts by status and by product/status.
func (s *Service) ServicesReport(ctx context.Context) (*ServicesReportData, error) {
	byStatus, err := s.d.Dashboard.ServicesReport(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if byStatus == nil {
		byStatus = map[string]int64{}
	}
	byProduct, err := s.d.Dashboard.ServicesByProduct(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &ServicesReportData{ByStatus: byStatus, ByProduct: byProduct}, nil
}

// Staff management

// ListStaff lists staff users (Search matches email, Status filters exact).
func (s *Service) ListStaff(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error) {
	users, total, err := s.d.Staff.ListStaff(ctx, p)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return users, total, nil
}

// OnlineStaff returns admin/staff users active within the presence window,
// backing the HostPanel "Staff Online" sidebar/dashboard widget.
func (s *Service) OnlineStaff(ctx context.Context) ([]ports.PresenceEntry, error) {
	if s.d.Presence == nil {
		return []ports.PresenceEntry{}, nil
	}
	entries, err := s.d.Presence.List(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return entries, nil
}

// validatePermissions checks every permission key against the known module
// whitelist.
func validatePermissions(perms map[string]bool) error {
	var details []apperr.FieldError
	for key := range perms {
		if !KnownPermissionModules[key] {
			details = append(details, apperr.FieldError{
				Field: "permissions." + key, Message: "unknown permission module",
			})
		}
	}
	if len(details) > 0 {
		return apperr.Validation("invalid permissions", details...)
	}
	return nil
}

func marshalPermissions(perms map[string]bool) json.RawMessage {
	if perms == nil {
		perms = map[string]bool{}
	}
	raw, _ := json.Marshal(perms)
	return raw
}

// CreateStaff creates a user with role=staff and the given permissions.
func (s *Service) CreateStaff(ctx context.Context, actorUserID int64, in CreateStaffInput) (*domain.User, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if err := validatePermissions(in.Permissions); err != nil {
		return nil, err
	}

	existing, err := s.d.Users.GetByEmail(ctx, in.Email)
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		return nil, apperr.Internal(err)
	}
	if existing != nil {
		return nil, apperr.Conflict("email already in use")
	}

	hash, err := s.d.Hasher.Hash(in.Password)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	locale := in.Locale
	if locale == "" {
		locale = "id"
	}
	user := &domain.User{
		Email:        in.Email,
		PasswordHash: hash,
		Role:         domain.RoleStaff,
		Status:       domain.UserActive,
		Permissions:  marshalPermissions(in.Permissions),
		Locale:       locale,
	}
	if err := s.d.Users.Create(ctx, user); err != nil {
		if e := apperr.From(err); e.Code != apperr.CodeInternal {
			return nil, e
		}
		return nil, apperr.Internal(err)
	}

	s.d.AuditLog.Log(ctx, actorUserID, "staff.create", "user", user.ID, nil, map[string]any{
		"email":       user.Email,
		"permissions": in.Permissions,
		"locale":      locale,
	})
	return user, nil
}

// GetStaff returns a staff user by id. Non-staff users (admins, clients)
// yield NOT_FOUND so this surface never manages them.
func (s *Service) GetStaff(ctx context.Context, id int64) (*domain.User, error) {
	user, err := s.d.Users.GetByID(ctx, id)
	if err != nil {
		if apperr.From(err).Code == apperr.CodeNotFound {
			return nil, apperr.NotFound("staff user")
		}
		return nil, apperr.Internal(err)
	}
	if user == nil || user.Role != domain.RoleStaff {
		return nil, apperr.NotFound("staff user")
	}
	return user, nil
}

// UpdateStaff patches permissions/status/locale and optionally the password
// of a staff user.
func (s *Service) UpdateStaff(ctx context.Context, actorUserID, id int64, in UpdateStaffInput) (*domain.User, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if in.Permissions != nil {
		if err := validatePermissions(in.Permissions); err != nil {
			return nil, err
		}
	}

	user, err := s.GetStaff(ctx, id)
	if err != nil {
		return nil, err
	}

	before := map[string]any{
		"status":      user.Status,
		"permissions": user.Permissions,
		"locale":      user.Locale,
	}

	if in.Permissions != nil {
		user.Permissions = marshalPermissions(in.Permissions)
	}
	if in.Status != nil {
		user.Status = domain.UserStatus(*in.Status)
	}
	if in.Locale != nil {
		user.Locale = *in.Locale
	}
	if err := s.d.Users.Update(ctx, user); err != nil {
		return nil, apperr.Internal(err)
	}

	if in.Password != nil {
		hash, err := s.d.Hasher.Hash(*in.Password)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if err := s.d.Users.UpdatePassword(ctx, user.ID, hash); err != nil {
			return nil, apperr.Internal(err)
		}
	}

	s.d.AuditLog.Log(ctx, actorUserID, "staff.update", "user", user.ID, before, map[string]any{
		"status":           user.Status,
		"permissions":      user.Permissions,
		"locale":           user.Locale,
		"password_changed": in.Password != nil,
	})
	return user, nil
}

// DeactivateStaff sets a staff user's status to inactive (idempotent).
func (s *Service) DeactivateStaff(ctx context.Context, actorUserID, id int64) (*domain.User, error) {
	user, err := s.GetStaff(ctx, id)
	if err != nil {
		return nil, err
	}
	if user.Status == domain.UserInactive {
		return user, nil
	}
	before := map[string]any{"status": user.Status}
	user.Status = domain.UserInactive
	if err := s.d.Users.Update(ctx, user); err != nil {
		return nil, apperr.Internal(err)
	}
	s.d.AuditLog.Log(ctx, actorUserID, "staff.deactivate", "user", user.ID,
		before, map[string]any{"status": user.Status})
	return user, nil
}

// Logs

// AuditLogs lists audit rows filtered by actor/entity/action/date.
func (s *Service) AuditLogs(ctx context.Context, f AuditLogFilter) ([]domain.AuditLog, int64, error) {
	if f.To != nil {
		end := truncateDate(*f.To).AddDate(0, 0, 1) // inclusive date -> exclusive bound
		f.To = &end
	}
	if f.From != nil {
		start := truncateDate(*f.From)
		f.From = &start
	}
	rows, total, err := s.d.Logs.ListAudit(ctx, f)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return rows, total, nil
}

// EmailLogs lists email log rows (Search matches to_email, Status exact).
func (s *Service) EmailLogs(ctx context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
	rows, total, err := s.d.EmailLogs.List(ctx, p)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return rows, total, nil
}

// IntegrationLogs lists integration log rows filtered by provider/success.
func (s *Service) IntegrationLogs(ctx context.Context, f IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
	rows, total, err := s.d.Logs.ListIntegration(ctx, f)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return rows, total, nil
}

// Pending Module Actions (WHMCS-style "Module Queue")

// PendingModuleActions lists provisioning/domain-registrar jobs stuck
// archived (exhausted every automatic retry) or still auto-retrying, that an
// admin can inspect and manually retry or dismiss.
func (s *Service) PendingModuleActions(ctx context.Context, f ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
	rows, total, err := s.d.Jobs.ListModuleActions(ctx, f)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return rows, total, nil
}

// RetryModuleAction re-runs one pending module action immediately instead of
// waiting for its next scheduled backoff attempt (or, for an archived task,
// never running again at all without this).
func (s *Service) RetryModuleAction(ctx context.Context, actorUserID int64, queue, id string) error {
	if err := s.d.Jobs.RetryModuleAction(ctx, queue, id); err != nil {
		return err
	}
	s.d.AuditLog.Log(ctx, actorUserID, "module_action.retry", "job", 0,
		nil, map[string]any{"queue": queue, "id": id})
	return nil
}

// DeleteModuleAction dismisses one pending module action without retrying
// it (e.g. the admin resolved it manually outside the system).
func (s *Service) DeleteModuleAction(ctx context.Context, actorUserID int64, queue, id string) error {
	if err := s.d.Jobs.DeleteModuleAction(ctx, queue, id); err != nil {
		return err
	}
	s.d.AuditLog.Log(ctx, actorUserID, "module_action.delete", "job", 0,
		nil, map[string]any{"queue": queue, "id": id})
	return nil
}

// DismissAllModuleActions dismisses every module action matching f (the
// currently-applied type/state filter), without retrying any of them.
func (s *Service) DismissAllModuleActions(ctx context.Context, actorUserID int64, f ports.ModuleActionFilter) (int, error) {
	n, err := s.d.Jobs.DismissAllModuleActions(ctx, f)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	s.d.AuditLog.Log(ctx, actorUserID, "module_action.dismiss_all", "job", 0,
		nil, map[string]any{"type": f.Type, "state": f.State, "count": n})
	return n, nil
}

// Gateways

// Gateways returns the duitku gateway configuration with secrets masked to
// presence booleans. Settings values fall back to the ENV-derived defaults.
// APIKeySet is true if either the DB (encrypted) or the environment has one.
func (s *Service) Gateways(ctx context.Context) (*GatewaysConfig, error) {
	mode := s.d.Secrets.DuitkuMode
	if mode == "" {
		mode = "sandbox"
	}
	merchantCode, err := s.d.Settings.GetString(ctx, SettingDuitkuMerchantCode, s.d.Secrets.DuitkuMerchantCode)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	mode, err = s.d.Settings.GetString(ctx, SettingDuitkuMode, mode)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	baseURL, err := s.d.Settings.GetString(ctx, SettingDuitkuBaseURL, s.d.Secrets.DuitkuBaseURL)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	apiKeyEnc, err := s.d.Settings.GetString(ctx, SettingDuitkuAPIKeyEnc, "")
	if err != nil {
		return nil, apperr.Internal(err)
	}
	manual, err := s.manualGatewayConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &GatewaysConfig{
		Duitku: DuitkuGatewayConfig{
			MerchantCode: merchantCode,
			Mode:         mode,
			BaseURL:      baseURL,
			APIKeySet:    apiKeyEnc != "" || s.d.Secrets.DuitkuAPIKeySet,
		},
		Manual: *manual,
	}, nil
}

// manualGatewayConfig reads the manual bank-transfer config; an unset key
// leaves the zero value (disabled, no accounts) per SettingsRepo.GetJSON's
// "missing keys leave out untouched" contract.
func (s *Service) manualGatewayConfig(ctx context.Context) (*ManualGatewayConfig, error) {
	var cfg ManualGatewayConfig
	if err := s.d.Settings.GetJSON(ctx, SettingManualGatewayConfig, &cfg); err != nil {
		return nil, apperr.Internal(err)
	}
	return &cfg, nil
}

// UpdateGateways stores the duitku configuration (merchant code, mode, base
// URL override, and optionally a new/cleared encrypted API key) in settings
// and audits the change. The API key itself is never included in the audit
// log snapshot.
func (s *Service) UpdateGateways(ctx context.Context, actorUserID int64, in UpdateGatewaysInput) (*GatewaysConfig, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	before, err := s.Gateways(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.d.Settings.Set(ctx, SettingDuitkuMerchantCode, in.MerchantCode); err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.d.Settings.Set(ctx, SettingDuitkuMode, in.Mode); err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.d.Settings.Set(ctx, SettingDuitkuBaseURL, strings.TrimSpace(in.BaseURL)); err != nil {
		return nil, apperr.Internal(err)
	}
	if in.APIKey != nil {
		enc := ""
		if *in.APIKey != "" {
			enc, err = s.d.Encryptor.Encrypt(*in.APIKey)
			if err != nil {
				return nil, apperr.Internal(err)
			}
		}
		if err := s.d.Settings.Set(ctx, SettingDuitkuAPIKeyEnc, enc); err != nil {
			return nil, apperr.Internal(err)
		}
	}

	s.d.AuditLog.Log(ctx, actorUserID, "gateways.update", "gateways", 0,
		map[string]any{"merchant_code": before.Duitku.MerchantCode, "mode": before.Duitku.Mode, "base_url": before.Duitku.BaseURL, "api_key_set": before.Duitku.APIKeySet},
		map[string]any{"merchant_code": in.MerchantCode, "mode": in.Mode, "base_url": in.BaseURL, "api_key_changed": in.APIKey != nil})
	return s.Gateways(ctx)
}

// UpdateManualGateway stores the manual bank-transfer gateway config
// (enabled flag, bank accounts, instructions) and audits the change. Nothing
// here is secret, so - unlike UpdateGateways' API key - the full new value
// is safe to include in the audit log.
func (s *Service) UpdateManualGateway(ctx context.Context, actorUserID int64, in UpdateManualGatewayInput) (*ManualGatewayConfig, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	before, err := s.manualGatewayConfig(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]ports.BankAccount, len(in.Accounts))
	for i, a := range in.Accounts {
		accounts[i] = ports.BankAccount{
			BankName:      strings.TrimSpace(a.BankName),
			AccountNumber: strings.TrimSpace(a.AccountNumber),
			AccountHolder: strings.TrimSpace(a.AccountHolder),
		}
	}
	cfg := ManualGatewayConfig{
		Enabled:      in.Enabled,
		Accounts:     accounts,
		Instructions: strings.TrimSpace(in.Instructions),
	}
	if err := s.d.Settings.Set(ctx, SettingManualGatewayConfig, cfg); err != nil {
		return nil, apperr.Internal(err)
	}
	s.d.AuditLog.Log(ctx, actorUserID, "gateways.manual.update", "gateways", 0,
		map[string]any{"enabled": before.Enabled, "account_count": len(before.Accounts)},
		map[string]any{"enabled": cfg.Enabled, "account_count": len(cfg.Accounts)})
	return &cfg, nil
}

// Housekeeping

// Housekeep purges integration_logs and email_log rows older than 90 days
// and audit_logs older than 365 days, returning the total deleted count.
// Consumed by the worker's cron:housekeeping handler (under a Redis lock).
func (s *Service) Housekeep(ctx context.Context) (int, error) {
	now := s.d.Clock.Now().UTC()
	logsBefore := now.AddDate(0, 0, -logRetentionDays)
	auditBefore := now.AddDate(0, 0, -auditRetentionDays)

	nIntegration, err := s.d.Logs.PurgeIntegrationLogsBefore(ctx, logsBefore)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	nEmail, err := s.d.Logs.PurgeEmailLogBefore(ctx, logsBefore)
	if err != nil {
		return int(nIntegration), apperr.Internal(err)
	}
	nAudit, err := s.d.Logs.PurgeAuditLogsBefore(ctx, auditBefore)
	if err != nil {
		return int(nIntegration + nEmail), apperr.Internal(err)
	}
	return int(nIntegration + nEmail + nAudit), nil
}
