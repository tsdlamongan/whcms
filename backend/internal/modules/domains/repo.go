package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the PostgreSQL unique_violation SQLSTATE.
const pgUniqueViolation = "23505"

const domainCols = `id, client_id, registrar_id, name, status, registration_date, expiry_date,
	next_due_date, recurring_amount, billing_cycle, auto_renew, nameservers, epp_code_enc,
	id_protection, dns_management_enabled, email_forwarding_enabled, registrar_meta, created_at, updated_at`

// Repo is the pgx ports.DomainRepo.
type Repo struct {
	db *db.DB
}

var _ ports.DomainRepo = (*Repo)(nil)

// NewRepo builds the domains Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDomain(row rowScanner) (*domain.Domain, error) {
	var d domain.Domain
	err := row.Scan(
		&d.ID, &d.ClientID, &d.RegistrarID, &d.Name, &d.Status,
		&d.RegistrationDate, &d.ExpiryDate, &d.NextDueDate,
		&d.RecurringAmount, &d.BillingCycle, &d.AutoRenew,
		&d.Nameservers, &d.EPPCodeEnc, &d.IDProtection,
		&d.DNSManagementEnabled, &d.EmailForwardingEnabled, &d.RegistrarMeta,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func jsonbOr(raw []byte, def string) []byte {
	if len(raw) == 0 {
		return []byte(def)
	}
	return raw
}

// Create inserts a domain row; duplicate non-cancelled names -> CONFLICT.
func (r *Repo) Create(ctx context.Context, d *domain.Domain) error {
	status := d.Status
	if status == "" {
		status = domain.DomainPending
	}
	cycle := d.BillingCycle
	if cycle == "" {
		cycle = domain.CycleAnnually
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO domains (client_id, registrar_id, name, status, registration_date,
			expiry_date, next_due_date, recurring_amount, billing_cycle, auto_renew,
			nameservers, epp_code_enc, id_protection, dns_management_enabled,
			email_forwarding_enabled, registrar_meta)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id, created_at, updated_at`,
		d.ClientID, d.RegistrarID, d.Name, status, d.RegistrationDate,
		d.ExpiryDate, d.NextDueDate, d.RecurringAmount, cycle, d.AutoRenew,
		jsonbOr(d.Nameservers, "[]"), d.EPPCodeEnc, d.IDProtection,
		d.DNSManagementEnabled, d.EmailForwardingEnabled,
		jsonbOr(d.RegistrarMeta, "{}"),
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict("domain " + d.Name + " already exists")
		}
		return fmt.Errorf("domains: create %s: %w", d.Name, err)
	}
	d.Status = status
	d.BillingCycle = cycle
	return nil
}

// GetByID fetches one domain.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Domain, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+domainCols+` FROM domains WHERE id = $1`, id)
	d, err := scanDomain(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("domain")
	}
	if err != nil {
		return nil, fmt.Errorf("domains: get %d: %w", id, err)
	}
	return d, nil
}

// GetByName fetches the non-cancelled domain with this name.
func (r *Repo) GetByName(ctx context.Context, name string) (*domain.Domain, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+domainCols+` FROM domains WHERE name = $1 AND status <> 'cancelled'`, name)
	d, err := scanDomain(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("domain")
	}
	if err != nil {
		return nil, fmt.Errorf("domains: get by name %s: %w", name, err)
	}
	return d, nil
}

// GetByIDForUpdate row-locks and returns the domain; call inside WithinTx.
func (r *Repo) GetByIDForUpdate(ctx context.Context, id int64) (*domain.Domain, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+domainCols+` FROM domains WHERE id = $1 FOR UPDATE`, id)
	d, err := scanDomain(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("domain")
	}
	if err != nil {
		return nil, fmt.Errorf("domains: get for update %d: %w", id, err)
	}
	return d, nil
}

// GetByIDs returns the domains matching ids (order not guaranteed); ids not
// found are simply omitted.
func (r *Repo) GetByIDs(ctx context.Context, ids []int64) ([]domain.Domain, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+domainCols+` FROM domains WHERE id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("domains: get by ids: %w", err)
	}
	defer rows.Close()
	return collectDomains(rows)
}

// Update persists all mutable domain fields.
func (r *Repo) Update(ctx context.Context, d *domain.Domain) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domains SET
			client_id = $2, registrar_id = $3, status = $4, registration_date = $5,
			expiry_date = $6, next_due_date = $7, recurring_amount = $8,
			billing_cycle = $9, auto_renew = $10, nameservers = $11,
			epp_code_enc = $12, id_protection = $13, dns_management_enabled = $14,
			email_forwarding_enabled = $15, registrar_meta = $16
		WHERE id = $1`,
		d.ID, d.ClientID, d.RegistrarID, d.Status, d.RegistrationDate,
		d.ExpiryDate, d.NextDueDate, d.RecurringAmount, d.BillingCycle,
		d.AutoRenew, jsonbOr(d.Nameservers, "[]"), d.EPPCodeEnc,
		d.IDProtection, d.DNSManagementEnabled, d.EmailForwardingEnabled,
		jsonbOr(d.RegistrarMeta, "{}"))
	if err != nil {
		return fmt.Errorf("domains: update %d: %w", d.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("domain")
	}
	return nil
}

// UpdateStatus sets only the status column.
func (r *Repo) UpdateStatus(ctx context.Context, id int64, status domain.DomainStatus) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE domains SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("domains: update status %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("domain")
	}
	return nil
}

// List returns domains with optional status filter and name search.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error) {
	return r.list(ctx, p, p.ClientID)
}

// ListByClient returns one client's domains.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error) {
	return r.list(ctx, p, clientID)
}

func (r *Repo) list(ctx context.Context, p ports.ListParams, clientID int64) ([]domain.Domain, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	if clientID > 0 {
		args = append(args, clientID)
		where += ` AND client_id = $` + strconv.Itoa(len(args))
	}
	if p.Status != "" {
		args = append(args, p.Status)
		where += ` AND status = $` + strconv.Itoa(len(args))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		where += ` AND name ILIKE $` + strconv.Itoa(len(args))
	}

	var total int64
	if err := r.db.Querier(ctx).
		QueryRow(ctx, `SELECT count(*) FROM domains`+where, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("domains: count: %w", err)
	}

	args = append(args, p.Limit(), p.Offset())
	query := `SELECT ` + domainCols + ` FROM domains` + where +
		` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args)-1) +
		` OFFSET $` + strconv.Itoa(len(args))
	rows, err := r.db.Querier(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("domains: list: %w", err)
	}
	defer rows.Close()

	out, err := collectDomains(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListRenewalsDue returns active auto_renew domains with next_due_date <= before
// (consumed by billing's renewal-invoice cron).
func (r *Repo) ListRenewalsDue(ctx context.Context, before time.Time) ([]domain.Domain, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+domainCols+` FROM domains
		WHERE status = 'active' AND auto_renew
		  AND next_due_date IS NOT NULL AND next_due_date <= $1
		ORDER BY next_due_date ASC, id ASC`, before)
	if err != nil {
		return nil, fmt.Errorf("domains: renewals due: %w", err)
	}
	defer rows.Close()
	return collectDomains(rows)
}

// ListForSync returns syncable domains (active, pending_transfer or expired),
// least-recently-updated first, bounded by limit.
func (r *Repo) ListForSync(ctx context.Context, limit int) ([]domain.Domain, error) {
	if limit <= 0 {
		limit = syncBatchLimit
	}
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+domainCols+` FROM domains
		WHERE status IN ('active','pending_transfer','expired')
		ORDER BY updated_at ASC, id ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("domains: list for sync: %w", err)
	}
	defer rows.Close()
	return collectDomains(rows)
}

func collectDomains(rows pgx.Rows) ([]domain.Domain, error) {
	var out []domain.Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("domains: scan: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("domains: rows: %w", err)
	}
	return out, nil
}

// RegistrarRepo

const registrarCols = `id, name, active, config, reseller_id, api_key_enc, base_url, created_at, updated_at`

// RegistrarRepo is the pgx ports.RegistrarRepo.
type RegistrarRepo struct {
	db *db.DB
}

var _ ports.RegistrarRepo = (*RegistrarRepo)(nil)

// NewRegistrarRepo builds the RegistrarRepo.
func NewRegistrarRepo(d *db.DB) *RegistrarRepo { return &RegistrarRepo{db: d} }

func scanRegistrar(row rowScanner) (*domain.Registrar, error) {
	var reg domain.Registrar
	err := row.Scan(&reg.ID, &reg.Name, &reg.Active, &reg.Config, &reg.ResellerID, &reg.APIKeyEnc, &reg.BaseURL, &reg.CreatedAt, &reg.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &reg, nil
}

// GetByID fetches one registrar row.
func (r *RegistrarRepo) GetByID(ctx context.Context, id int64) (*domain.Registrar, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+registrarCols+` FROM registrars WHERE id = $1`, id)
	reg, err := scanRegistrar(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("registrar")
	}
	if err != nil {
		return nil, fmt.Errorf("registrars: get %d: %w", id, err)
	}
	return reg, nil
}

// GetByName fetches one registrar row by unique name (e.g. "rdash").
func (r *RegistrarRepo) GetByName(ctx context.Context, name string) (*domain.Registrar, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+registrarCols+` FROM registrars WHERE name = $1`, name)
	reg, err := scanRegistrar(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("registrar")
	}
	if err != nil {
		return nil, fmt.Errorf("registrars: get by name %s: %w", name, err)
	}
	return reg, nil
}

// Update persists active/config/reseller_id/api_key_enc.
func (r *RegistrarRepo) Update(ctx context.Context, reg *domain.Registrar) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE registrars SET active = $2, config = $3, reseller_id = $4, api_key_enc = $5, base_url = $6 WHERE id = $1`,
		reg.ID, reg.Active, jsonbOr(reg.Config, "{}"), reg.ResellerID, reg.APIKeyEnc, reg.BaseURL)
	if err != nil {
		return fmt.Errorf("registrars: update %d: %w", reg.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("registrar")
	}
	return nil
}

// List returns all registrar rows ordered by id.
func (r *RegistrarRepo) List(ctx context.Context) ([]domain.Registrar, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+registrarCols+` FROM registrars ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("registrars: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Registrar
	for rows.Next() {
		reg, err := scanRegistrar(rows)
		if err != nil {
			return nil, fmt.Errorf("registrars: scan: %w", err)
		}
		out = append(out, *reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registrars: rows: %w", err)
	}
	return out, nil
}

// TLDPricingRepo

const tldPricingCols = `id, tld, registrar_id, active, min_years, max_years,
	register_prices, renew_prices, transfer_price, restore_price, created_at, updated_at`

// TLDPricingRepo is the pgx ports.TLDPricingRepo.
type TLDPricingRepo struct {
	db *db.DB
}

var _ ports.TLDPricingRepo = (*TLDPricingRepo)(nil)

// NewTLDPricingRepo builds the TLDPricingRepo.
func NewTLDPricingRepo(d *db.DB) *TLDPricingRepo { return &TLDPricingRepo{db: d} }

func scanTLDPricing(row rowScanner) (*domain.TLDPricing, error) {
	var p domain.TLDPricing
	var registerRaw, renewRaw []byte
	err := row.Scan(&p.ID, &p.TLD, &p.RegistrarID, &p.Active, &p.MinYears, &p.MaxYears,
		&registerRaw, &renewRaw, &p.TransferPrice, &p.RestorePrice, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(jsonbOr(registerRaw, "{}"), &p.RegisterPrices); err != nil {
		return nil, fmt.Errorf("tld_pricing: decode register_prices: %w", err)
	}
	if err := json.Unmarshal(jsonbOr(renewRaw, "{}"), &p.RenewPrices); err != nil {
		return nil, fmt.Errorf("tld_pricing: decode renew_prices: %w", err)
	}
	return &p, nil
}

// GetByID fetches one TLD pricing row.
func (r *TLDPricingRepo) GetByID(ctx context.Context, id int64) (*domain.TLDPricing, error) {
	row := r.db.Querier(ctx).QueryRow(ctx, `SELECT `+tldPricingCols+` FROM tld_pricing WHERE id = $1`, id)
	p, err := scanTLDPricing(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("tld pricing")
	}
	if err != nil {
		return nil, fmt.Errorf("tld_pricing: get %d: %w", id, err)
	}
	return p, nil
}

// GetByTLD fetches one TLD pricing row by its extension (e.g. "com", "co.id").
func (r *TLDPricingRepo) GetByTLD(ctx context.Context, tld string) (*domain.TLDPricing, error) {
	row := r.db.Querier(ctx).QueryRow(ctx, `SELECT `+tldPricingCols+` FROM tld_pricing WHERE tld = $1`, tld)
	p, err := scanTLDPricing(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("tld pricing")
	}
	if err != nil {
		return nil, fmt.Errorf("tld_pricing: get by tld %s: %w", tld, err)
	}
	return p, nil
}

// Create inserts a new TLD pricing row; duplicate tld -> CONFLICT.
func (r *TLDPricingRepo) Create(ctx context.Context, p *domain.TLDPricing) error {
	registerRaw, err := json.Marshal(p.RegisterPrices)
	if err != nil {
		return fmt.Errorf("tld_pricing: encode register_prices: %w", err)
	}
	renewRaw, err := json.Marshal(p.RenewPrices)
	if err != nil {
		return fmt.Errorf("tld_pricing: encode renew_prices: %w", err)
	}
	err = r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO tld_pricing (tld, registrar_id, active, min_years, max_years,
			register_prices, renew_prices, transfer_price, restore_price)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`,
		p.TLD, p.RegistrarID, p.Active, p.MinYears, p.MaxYears,
		registerRaw, renewRaw, p.TransferPrice, p.RestorePrice,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict("tld " + p.TLD + " already has a pricing row")
		}
		return fmt.Errorf("tld_pricing: create %s: %w", p.TLD, err)
	}
	return nil
}

// Update persists all mutable TLD pricing fields.
func (r *TLDPricingRepo) Update(ctx context.Context, p *domain.TLDPricing) error {
	registerRaw, err := json.Marshal(p.RegisterPrices)
	if err != nil {
		return fmt.Errorf("tld_pricing: encode register_prices: %w", err)
	}
	renewRaw, err := json.Marshal(p.RenewPrices)
	if err != nil {
		return fmt.Errorf("tld_pricing: encode renew_prices: %w", err)
	}
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE tld_pricing SET
			tld = $2, registrar_id = $3, active = $4, min_years = $5, max_years = $6,
			register_prices = $7, renew_prices = $8, transfer_price = $9, restore_price = $10
		WHERE id = $1`,
		p.ID, p.TLD, p.RegistrarID, p.Active, p.MinYears, p.MaxYears,
		registerRaw, renewRaw, p.TransferPrice, p.RestorePrice)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict("tld " + p.TLD + " already has a pricing row")
		}
		return fmt.Errorf("tld_pricing: update %d: %w", p.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("tld pricing")
	}
	return nil
}

// Delete removes a TLD pricing row.
func (r *TLDPricingRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM tld_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("tld_pricing: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("tld pricing")
	}
	return nil
}

// List returns all TLD pricing rows ordered by tld.
func (r *TLDPricingRepo) List(ctx context.Context) ([]domain.TLDPricing, error) {
	return r.listWhere(ctx, "")
}

// ListActive returns only TLD pricing rows offered for sale.
func (r *TLDPricingRepo) ListActive(ctx context.Context) ([]domain.TLDPricing, error) {
	return r.listWhere(ctx, " WHERE active = TRUE")
}

func (r *TLDPricingRepo) listWhere(ctx context.Context, where string) ([]domain.TLDPricing, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `SELECT `+tldPricingCols+` FROM tld_pricing`+where+` ORDER BY tld`)
	if err != nil {
		return nil, fmt.Errorf("tld_pricing: list: %w", err)
	}
	defer rows.Close()

	var out []domain.TLDPricing
	for rows.Next() {
		p, err := scanTLDPricing(rows)
		if err != nil {
			return nil, fmt.Errorf("tld_pricing: scan: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tld_pricing: rows: %w", err)
	}
	return out, nil
}

// PremiumDomainPricingRepo

const premiumPricingCols = `id, domain_name, register_price, renew_price, transfer_price, created_at, updated_at`

// PremiumDomainPricingRepo is the pgx ports.PremiumDomainPricingRepo.
type PremiumDomainPricingRepo struct {
	db *db.DB
}

var _ ports.PremiumDomainPricingRepo = (*PremiumDomainPricingRepo)(nil)

// NewPremiumDomainPricingRepo builds the PremiumDomainPricingRepo.
func NewPremiumDomainPricingRepo(d *db.DB) *PremiumDomainPricingRepo {
	return &PremiumDomainPricingRepo{db: d}
}

func scanPremiumPricing(row rowScanner) (*domain.PremiumDomainPricing, error) {
	var p domain.PremiumDomainPricing
	err := row.Scan(&p.ID, &p.DomainName, &p.RegisterPrice, &p.RenewPrice, &p.TransferPrice, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByName fetches the premium pricing row for one exact domain name.
func (r *PremiumDomainPricingRepo) GetByName(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
	row := r.db.Querier(ctx).QueryRow(ctx, `SELECT `+premiumPricingCols+` FROM premium_domain_pricing WHERE domain_name = $1`, name)
	p, err := scanPremiumPricing(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("premium domain pricing")
	}
	if err != nil {
		return nil, fmt.Errorf("premium_domain_pricing: get by name %s: %w", name, err)
	}
	return p, nil
}

// Create inserts a new premium pricing row; duplicate domain_name -> CONFLICT.
func (r *PremiumDomainPricingRepo) Create(ctx context.Context, p *domain.PremiumDomainPricing) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO premium_domain_pricing (domain_name, register_price, renew_price, transfer_price)
		VALUES ($1,$2,$3,$4)
		RETURNING id, created_at, updated_at`,
		p.DomainName, p.RegisterPrice, p.RenewPrice, p.TransferPrice,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict("domain " + p.DomainName + " already has premium pricing")
		}
		return fmt.Errorf("premium_domain_pricing: create %s: %w", p.DomainName, err)
	}
	return nil
}

// Update persists all mutable premium pricing fields.
func (r *PremiumDomainPricingRepo) Update(ctx context.Context, p *domain.PremiumDomainPricing) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE premium_domain_pricing SET
			domain_name = $2, register_price = $3, renew_price = $4, transfer_price = $5
		WHERE id = $1`,
		p.ID, p.DomainName, p.RegisterPrice, p.RenewPrice, p.TransferPrice)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict("domain " + p.DomainName + " already has premium pricing")
		}
		return fmt.Errorf("premium_domain_pricing: update %d: %w", p.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("premium domain pricing")
	}
	return nil
}

// Delete removes a premium pricing row.
func (r *PremiumDomainPricingRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM premium_domain_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("premium_domain_pricing: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("premium domain pricing")
	}
	return nil
}

// List returns all premium pricing rows ordered by domain_name.
func (r *PremiumDomainPricingRepo) List(ctx context.Context) ([]domain.PremiumDomainPricing, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `SELECT `+premiumPricingCols+` FROM premium_domain_pricing ORDER BY domain_name`)
	if err != nil {
		return nil, fmt.Errorf("premium_domain_pricing: list: %w", err)
	}
	defer rows.Close()

	var out []domain.PremiumDomainPricing
	for rows.Next() {
		p, err := scanPremiumPricing(rows)
		if err != nil {
			return nil, fmt.Errorf("premium_domain_pricing: scan: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("premium_domain_pricing: rows: %w", err)
	}
	return out, nil
}

// PremiumLengthPricingRepo

const premiumLengthPricingCols = `id, tld, char_length, price, created_at, updated_at`

// PremiumLengthPricingRepo is the pgx ports.PremiumLengthPricingRepo.
type PremiumLengthPricingRepo struct {
	db *db.DB
}

var _ ports.PremiumLengthPricingRepo = (*PremiumLengthPricingRepo)(nil)

// NewPremiumLengthPricingRepo builds the PremiumLengthPricingRepo.
func NewPremiumLengthPricingRepo(d *db.DB) *PremiumLengthPricingRepo {
	return &PremiumLengthPricingRepo{db: d}
}

func scanPremiumLengthPricing(row rowScanner) (*domain.PremiumLengthPricing, error) {
	var p domain.PremiumLengthPricing
	err := row.Scan(&p.ID, &p.TLD, &p.CharLength, &p.Price, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByTLDAndLength fetches the premium length-tier row for one TLD + char count.
func (r *PremiumLengthPricingRepo) GetByTLDAndLength(ctx context.Context, tld string, charLength int) (*domain.PremiumLengthPricing, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+premiumLengthPricingCols+` FROM premium_length_pricing WHERE tld = $1 AND char_length = $2`,
		tld, charLength)
	p, err := scanPremiumLengthPricing(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("premium length pricing")
	}
	if err != nil {
		return nil, fmt.Errorf("premium_length_pricing: get by tld/length %s/%d: %w", tld, charLength, err)
	}
	return p, nil
}

// Create inserts a new premium length-tier row; duplicate (tld, char_length) -> CONFLICT.
func (r *PremiumLengthPricingRepo) Create(ctx context.Context, p *domain.PremiumLengthPricing) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO premium_length_pricing (tld, char_length, price)
		VALUES ($1,$2,$3)
		RETURNING id, created_at, updated_at`,
		p.TLD, p.CharLength, p.Price,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict(fmt.Sprintf("%s already has a %d-character pricing row", p.TLD, p.CharLength))
		}
		return fmt.Errorf("premium_length_pricing: create %s/%d: %w", p.TLD, p.CharLength, err)
	}
	return nil
}

// Update persists all mutable premium length-tier fields.
func (r *PremiumLengthPricingRepo) Update(ctx context.Context, p *domain.PremiumLengthPricing) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE premium_length_pricing SET tld = $2, char_length = $3, price = $4
		WHERE id = $1`,
		p.ID, p.TLD, p.CharLength, p.Price)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict(fmt.Sprintf("%s already has a %d-character pricing row", p.TLD, p.CharLength))
		}
		return fmt.Errorf("premium_length_pricing: update %d: %w", p.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("premium length pricing")
	}
	return nil
}

// Delete removes a premium length-tier row.
func (r *PremiumLengthPricingRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM premium_length_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("premium_length_pricing: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("premium length pricing")
	}
	return nil
}

// List returns all premium length-tier rows ordered by tld, char_length.
func (r *PremiumLengthPricingRepo) List(ctx context.Context) ([]domain.PremiumLengthPricing, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `SELECT `+premiumLengthPricingCols+` FROM premium_length_pricing ORDER BY tld, char_length`)
	if err != nil {
		return nil, fmt.Errorf("premium_length_pricing: list: %w", err)
	}
	defer rows.Close()

	var out []domain.PremiumLengthPricing
	for rows.Next() {
		p, err := scanPremiumLengthPricing(rows)
		if err != nil {
			return nil, fmt.Errorf("premium_length_pricing: scan: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("premium_length_pricing: rows: %w", err)
	}
	return out, nil
}

// DomainAddonRepo

const domainAddonCols = `id, key, name, price, active, created_at, updated_at`

// DomainAddonRepo is the pgx ports.DomainAddonRepo.
type DomainAddonRepo struct {
	db *db.DB
}

var _ ports.DomainAddonRepo = (*DomainAddonRepo)(nil)

// NewDomainAddonRepo builds the DomainAddonRepo.
func NewDomainAddonRepo(d *db.DB) *DomainAddonRepo { return &DomainAddonRepo{db: d} }

func scanDomainAddon(row rowScanner) (*domain.DomainAddon, error) {
	var a domain.DomainAddon
	err := row.Scan(&a.ID, &a.Key, &a.Name, &a.Price, &a.Active, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetByKey fetches one addon row by its fixed key.
func (r *DomainAddonRepo) GetByKey(ctx context.Context, key string) (*domain.DomainAddon, error) {
	row := r.db.Querier(ctx).QueryRow(ctx, `SELECT `+domainAddonCols+` FROM domain_addons WHERE key = $1`, key)
	a, err := scanDomainAddon(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("domain addon")
	}
	if err != nil {
		return nil, fmt.Errorf("domain_addons: get by key %s: %w", key, err)
	}
	return a, nil
}

// Update persists name/price/active for one addon row.
func (r *DomainAddonRepo) Update(ctx context.Context, a *domain.DomainAddon) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE domain_addons SET price = $2, active = $3 WHERE id = $1`,
		a.ID, a.Price, a.Active)
	if err != nil {
		return fmt.Errorf("domain_addons: update %d: %w", a.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("domain addon")
	}
	return nil
}

// List returns all 3 addon rows ordered by id (seed order).
func (r *DomainAddonRepo) List(ctx context.Context) ([]domain.DomainAddon, error) {
	return r.listWhere(ctx, "")
}

// ListActive returns only addon rows currently offered for sale.
func (r *DomainAddonRepo) ListActive(ctx context.Context) ([]domain.DomainAddon, error) {
	return r.listWhere(ctx, " WHERE active = TRUE")
}

func (r *DomainAddonRepo) listWhere(ctx context.Context, where string) ([]domain.DomainAddon, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `SELECT `+domainAddonCols+` FROM domain_addons`+where+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("domain_addons: list: %w", err)
	}
	defer rows.Close()

	var out []domain.DomainAddon
	for rows.Next() {
		a, err := scanDomainAddon(rows)
		if err != nil {
			return nil, fmt.Errorf("domain_addons: scan: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("domain_addons: rows: %w", err)
	}
	return out, nil
}
