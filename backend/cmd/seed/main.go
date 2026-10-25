// Command seed populates the freshly-migrated local "whmcs" database with
// the minimum fixtures the Fase 5 E2E flows need (docs/E2E.md §4):
//   - an admin user and a staff user (staff has no "settings" permission),
//   - a cpanel server + server group and a directadmin server + server group,
//     both pointing at the mockserver (localhost:9090),
//   - a shared_hosting product bound to the cpanel server group with
//     auto_setup=on_payment and monthly pricing.
//
// It does NOT seed a "domain" product: standalone domain orders need no
// product row, but a domain can only be ordered when its extension has an
// ACTIVE Domain Pricing row (internal/modules/orders/pricing.go's planDomain),
// so it seeds tld_pricing for the extensions the specs order under (see
// e2eSellableTLDs). The "rdash"
// registrar row inserted by migration 000002 is also left untouched: the
// RDash adapter (internal/integration/rdash, wired in
// internal/composition/build.go) takes its reseller_id/api_key/base_url from
// env vars (RDASH_RESELLER_ID/RDASH_API_KEY/RDASH_BASE_URL), never from the
// registrars.config JSONB column.
//
// Safe to re-run: every insert is guarded by a check-then-insert or
// ON CONFLICT DO NOTHING, matched on the natural unique key (email, slug,
// name, ...).
//
// Usage: go run ./cmd/seed (same env as cmd/api / cmd/worker; see docs/E2E.md §2).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/platform/config"
	"github.com/tsdlamongan/whcms/backend/internal/platform/crypto"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	adminEmail = "admin@e2e.test"
	adminPass  = "AdminE2E!2026"
	staffEmail = "staff@e2e.test"
	staffPass  = "StaffE2E!2026"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed: fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if len(cfg.EncryptionKey) != 32 {
		return fmt.Errorf("APP_ENCRYPTION_KEY must decode to exactly 32 bytes (got %d)", len(cfg.EncryptionKey))
	}

	ctx := context.Background()
	database, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer database.Close()

	hasher := crypto.NewPasswordHasher()
	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("encryptor: %w", err)
	}

	pool := database.Pool()

	// --- Admin user ---------------------------------------------------------
	adminID, err := seedUser(ctx, database, hasher, adminEmail, adminPass, "admin", `{}`)
	if err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	slog.Info("seeded admin user", "id", adminID, "email", adminEmail)

	// --- Staff user (every module EXCEPT settings) --------------------------
	staffPerms := map[string]bool{
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
		"reports":       true,
		"logs":          true,
		"staff":         true,
		"gateways":      true,
		"announcements": true,
		"knowledgebase": true,
		"network":       true,
		// "settings" deliberately omitted for the RBAC flow (PRD §13.2 flow 8).
	}
	staffPermsJSON, err := json.Marshal(staffPerms)
	if err != nil {
		return err
	}
	staffID, err := seedUser(ctx, database, hasher, staffEmail, staffPass, "staff", string(staffPermsJSON))
	if err != nil {
		return fmt.Errorf("seed staff: %w", err)
	}
	slog.Info("seeded staff user", "id", staffID, "email", staffEmail)

	// --- cpanel server group + server ----------------------------------------
	cpanelGroupID, err := seedServerGroup(ctx, pool, "E2E cPanel Group", "round_robin")
	if err != nil {
		return fmt.Errorf("seed cpanel server group: %w", err)
	}
	cpanelServerID, err := seedServer(ctx, pool, enc, serverSpec{
		GroupID:  cpanelGroupID,
		Name:     "E2E cPanel Mock",
		Module:   "cpanel",
		Hostname: "localhost",
		Port:     9090,
		Username: "e2eadmin",
		Token:    "e2e-cpanel-token",
		UseSSL:   false,
	})
	if err != nil {
		return fmt.Errorf("seed cpanel server: %w", err)
	}
	slog.Info("seeded cpanel server", "server_id", cpanelServerID, "group_id", cpanelGroupID)

	// --- directadmin server group + server -----------------------------------
	daGroupID, err := seedServerGroup(ctx, pool, "E2E DirectAdmin Group", "round_robin")
	if err != nil {
		return fmt.Errorf("seed directadmin server group: %w", err)
	}
	daServerID, err := seedServer(ctx, pool, enc, serverSpec{
		GroupID:   daGroupID,
		Name:      "E2E DirectAdmin Mock",
		Module:    "directadmin",
		Hostname:  "localhost",
		Port:      9090,
		Username:  "e2eadmin",
		Password:  "e2e-da-password",
		UseSSL:    false,
		IPAddress: "203.0.113.10", // TEST-NET-3 (RFC 5737); DA account creation requires a non-blank IP
	})
	if err != nil {
		return fmt.Errorf("seed directadmin server: %w", err)
	}
	slog.Info("seeded directadmin server", "server_id", daServerID, "group_id", daGroupID)

	// --- product group + shared_hosting product ------------------------------
	groupID, err := seedProductGroup(ctx, pool, "E2E Hosting", "e2e-hosting")
	if err != nil {
		return fmt.Errorf("seed product group: %w", err)
	}
	productID, err := seedProduct(ctx, pool, productSpec{
		GroupID:       groupID,
		Name:          "E2E Shared Hosting",
		Slug:          "e2e-shared-hosting",
		Type:          "shared_hosting",
		Module:        "cpanel",
		ServerGroupID: cpanelGroupID,
		PackageName:   "e2e_basic",
		AutoSetup:     "on_payment",
	})
	if err != nil {
		return fmt.Errorf("seed product: %w", err)
	}
	slog.Info("seeded shared_hosting product", "product_id", productID, "slug", "e2e-shared-hosting")

	if err := seedPricing(ctx, pool, productID, "monthly", 50000, 0); err != nil {
		return fmt.Errorf("seed pricing: %w", err)
	}
	slog.Info("seeded product pricing", "product_id", productID, "cycle", "monthly", "price", 50000)

	// --- sellable domain extensions ------------------------------------------
	// A domain can only be ordered when its extension has an ACTIVE Domain
	// Pricing row, so the specs' extensions are priced here at the RDash
	// mock's 150.000/year (1..10 years, linear).
	for _, tld := range e2eSellableTLDs {
		if err := seedTLDPricing(ctx, pool, tld, 150000); err != nil {
			return fmt.Errorf("seed tld pricing %s: %w", tld, err)
		}
	}
	slog.Info("seeded domain pricing", "tlds", e2eSellableTLDs)

	// --- portal feature fixtures (announcements / KB / network status) -------
	announcementID, err := seedAnnouncement(ctx, pool, adminID)
	if err != nil {
		return fmt.Errorf("seed announcement: %w", err)
	}
	slog.Info("seeded announcement", "id", announcementID, "slug", "welcome")

	kbCategoryID, err := seedKBCategory(ctx, pool, "Getting Started", "getting-started")
	if err != nil {
		return fmt.Errorf("seed kb category: %w", err)
	}
	kbArticleID, err := seedKBArticle(ctx, pool, kbCategoryID, adminID)
	if err != nil {
		return fmt.Errorf("seed kb article: %w", err)
	}
	slog.Info("seeded knowledgebase", "category_id", kbCategoryID, "article_id", kbArticleID)

	if err := seedNetworkIssues(ctx, pool); err != nil {
		return fmt.Errorf("seed network issues: %w", err)
	}
	slog.Info("seeded network issues", "active", "investigating", "resolved", "resolved")

	// --- dev CAPTCHA site key -----------------------------------------------
	// CAPTCHA stays disabled by default (migration 000009); this only pre-fills
	// the dev site key (Cloudflare's "always passes" test key) when unset so an
	// operator can flip it on in Admin -> Settings -> Security with no extra
	// setup. Never clobbers a real key already configured.
	if err := seedCaptchaSiteKey(ctx, pool); err != nil {
		return fmt.Errorf("seed captcha site key: %w", err)
	}

	slog.Info("seed complete",
		"admin_email", adminEmail, "admin_password", adminPass,
		"staff_email", staffEmail, "staff_password", staffPass,
		"product_slug", "e2e-shared-hosting", "product_id", productID,
		"cpanel_server_id", cpanelServerID, "directadmin_server_id", daServerID,
	)
	return nil
}

// seedCaptchaSiteKey sets the dev Turnstile "always passes" site key when the
// setting is currently empty (idempotent; never overwrites a configured key).
// The row itself is created by migration 000009 with an empty value.
func seedCaptchaSiteKey(ctx context.Context, pool *pgxpool.Pool) error {
	const devSiteKey = `"1x00000000000000000000AA"` // JSONB string literal
	_, err := pool.Exec(ctx, `
		UPDATE settings SET value = $1
		WHERE key = 'security.captcha_site_key' AND value IN ('""', 'null')`, devSiteKey)
	return err
}

// seedUser inserts a user with the given email/password/role/permissions if
// it doesn't already exist (matched by email); returns the user's id either
// way. Existing users are left untouched (idempotent re-run).
func seedUser(ctx context.Context, database *db.DB, hasher *crypto.PasswordHasher, email, password, role, permsJSON string) (int64, error) {
	pool := database.Pool()
	var id int64
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id)
	if err == nil {
		return id, nil
	}

	hash, herr := hasher.Hash(password)
	if herr != nil {
		return 0, herr
	}
	now := time.Now().UTC()
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, status, permissions, email_verified_at)
		VALUES ($1, $2, $3, 'active', $4::jsonb, $5)
		ON CONFLICT (email) DO NOTHING
		RETURNING id`,
		email, hash, role, permsJSON, now,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	// Lost the race against a concurrent seed run (ON CONFLICT DO NOTHING
	// with no row returned) - re-select.
	if serr := pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id); serr != nil {
		return 0, serr
	}
	return id, nil
}

func seedServerGroup(ctx context.Context, pool *pgxpool.Pool, name, strategy string) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM server_groups WHERE name = $1`, name).Scan(&id); err == nil {
		return id, nil
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO server_groups (name, strategy) VALUES ($1, $2)
		RETURNING id`, name, strategy).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

type serverSpec struct {
	GroupID   int64
	Name      string
	Module    string
	Hostname  string
	Port      int
	Username  string
	Password  string // directadmin
	Token     string // cpanel api token
	UseSSL    bool
	IPAddress string // required by DirectAdmin account creation; blank is fine for cpanel
}

func seedServer(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, spec serverSpec) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM servers WHERE name = $1`, spec.Name).Scan(&id); err == nil {
		return id, nil
	}

	var passwordEnc, tokenEnc string
	var err error
	if spec.Password != "" {
		passwordEnc, err = enc.Encrypt(spec.Password)
		if err != nil {
			return 0, err
		}
	}
	if spec.Token != "" {
		tokenEnc, err = enc.Encrypt(spec.Token)
		if err != nil {
			return 0, err
		}
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO servers (group_id, name, module, hostname, port, username,
			password_enc, api_token_enc, use_ssl, ip_address, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, TRUE)
		RETURNING id`,
		spec.GroupID, spec.Name, spec.Module, spec.Hostname, spec.Port, spec.Username,
		passwordEnc, tokenEnc, spec.UseSSL, spec.IPAddress,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func seedProductGroup(ctx context.Context, pool *pgxpool.Pool, name, slug string) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM product_groups WHERE slug = $1`, slug).Scan(&id); err == nil {
		return id, nil
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO product_groups (name, slug, sort, hidden)
		VALUES ($1, $2, 0, FALSE)
		RETURNING id`, name, slug).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

type productSpec struct {
	GroupID       int64
	Name          string
	Slug          string
	Type          string
	Module        string
	ServerGroupID int64
	PackageName   string
	AutoSetup     string
}

func seedProduct(ctx context.Context, pool *pgxpool.Pool, spec productSpec) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM products WHERE slug = $1`, spec.Slug).Scan(&id); err == nil {
		return id, nil
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO products (group_id, name, slug, description, type, module,
			server_group_id, package_name, auto_setup, stock_enabled, stock_qty,
			hidden, sort, welcome_email_template)
		VALUES ($1, $2, $3, '', $4, $5, $6, $7, $8, FALSE, 0, FALSE, 0, '')
		RETURNING id`,
		spec.GroupID, spec.Name, spec.Slug, spec.Type, spec.Module,
		spec.ServerGroupID, spec.PackageName, spec.AutoSetup,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func seedPricing(ctx context.Context, pool *pgxpool.Pool, productID int64, cycle string, price, setupFee int64) error {
	var id int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM product_pricing WHERE product_id = $1 AND cycle = $2 AND currency = 'IDR'`,
		productID, cycle).Scan(&id); err == nil {
		return nil
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO product_pricing (product_id, cycle, price, setup_fee, currency)
		VALUES ($1, $2, $3, $4, 'IDR')
		ON CONFLICT (product_id, cycle, currency) DO NOTHING`,
		productID, cycle, price, setupFee)
	return err
}

// e2eSellableTLDs are the extensions the E2E specs order domains under:
// com (generic), e2e.test (`<label>.e2e.test` fixtures) and co.id (the
// document lifecycle specs). my.id is deliberately NOT here: the TLD pricing
// import spec owns it (it deletes and re-imports that row), so a spec that
// needs it provisions its own via ensureSellableTld.
var e2eSellableTLDs = []string{"com", "e2e.test", "co.id"}

// seedTLDPricing creates an ACTIVE Domain Pricing row for tld on the rdash
// registrar: register price perYear * years for 1..10 years, the same renew
// and transfer price. Idempotent (never touches an existing row).
func seedTLDPricing(ctx context.Context, pool *pgxpool.Pool, tld string, perYear int64) error {
	register := make(map[string]int64, 10)
	renew := make(map[string]int64, 10)
	for y := int64(1); y <= 10; y++ {
		register[strconv.FormatInt(y, 10)] = perYear * y
		renew[strconv.FormatInt(y, 10)] = perYear * y
	}
	registerJSON, err := json.Marshal(register)
	if err != nil {
		return err
	}
	renewJSON, err := json.Marshal(renew)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO tld_pricing (tld, registrar_id, active, min_years, max_years, register_prices, renew_prices, transfer_price)
		SELECT $1, r.id, TRUE, 1, 10, $2::jsonb, $3::jsonb, $4
		FROM registrars r WHERE r.name = 'rdash'
		ON CONFLICT (tld) DO NOTHING`,
		tld, registerJSON, renewJSON, perYear)
	return err
}

// seedAnnouncement inserts one published portal announcement (slug "welcome"),
// published in the past so it surfaces on the public portal. Idempotent on slug.
func seedAnnouncement(ctx context.Context, pool *pgxpool.Pool, authorID int64) (int64, error) {
	const slug = "welcome"
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM announcements WHERE slug = $1`, slug).Scan(&id); err == nil {
		return id, nil
	}
	now := time.Now().UTC()
	err := pool.QueryRow(ctx, `
		INSERT INTO announcements (title, slug, body, published, published_at, author_id)
		VALUES ($1, $2, $3, TRUE, $4, $5)
		ON CONFLICT (slug) DO NOTHING
		RETURNING id`,
		"Welcome to our hosting portal", slug,
		"Thanks for choosing us. This is the first announcement on your portal.",
		now, authorID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	// Lost the race (ON CONFLICT DO NOTHING returned no row) - re-select.
	if serr := pool.QueryRow(ctx, `SELECT id FROM announcements WHERE slug = $1`, slug).Scan(&id); serr != nil {
		return 0, serr
	}
	return id, nil
}

// seedKBCategory inserts one visible KB category. Idempotent on slug.
func seedKBCategory(ctx context.Context, pool *pgxpool.Pool, name, slug string) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM kb_categories WHERE slug = $1`, slug).Scan(&id); err == nil {
		return id, nil
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO kb_categories (name, slug, description, sort, hidden)
		VALUES ($1, $2, $3, 0, FALSE)
		ON CONFLICT (slug) DO NOTHING
		RETURNING id`,
		name, slug, "Guides to help you get started.",
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if serr := pool.QueryRow(ctx, `SELECT id FROM kb_categories WHERE slug = $1`, slug).Scan(&id); serr != nil {
		return 0, serr
	}
	return id, nil
}

// seedKBArticle inserts one published KB article (slug "how-to-order") in the
// given category. Idempotent on slug.
func seedKBArticle(ctx context.Context, pool *pgxpool.Pool, categoryID, authorID int64) (int64, error) {
	const slug = "how-to-order"
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM kb_articles WHERE slug = $1`, slug).Scan(&id); err == nil {
		return id, nil
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO kb_articles (category_id, title, slug, body, published, sort, author_id)
		VALUES ($1, $2, $3, $4, TRUE, 0, $5)
		ON CONFLICT (slug) DO NOTHING
		RETURNING id`,
		categoryID, "How to place an order", slug,
		"Browse the product catalog, pick a plan, and complete checkout to place your first order.",
		authorID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if serr := pool.QueryRow(ctx, `SELECT id FROM kb_articles WHERE slug = $1`, slug).Scan(&id); serr != nil {
		return 0, serr
	}
	return id, nil
}

// seedNetworkIssues inserts one active (investigating) and one resolved network
// status entry. network_issues has no natural unique key, so each is guarded by
// a check-then-insert matched on title (like seedServerGroup).
func seedNetworkIssues(ctx context.Context, pool *pgxpool.Pool) error {
	now := time.Now().UTC()

	active := networkIssueSpec{
		Title:    "Investigating elevated latency",
		Body:     "We are investigating reports of elevated latency on some services.",
		Type:     "issue",
		Severity: "minor",
		Status:   "investigating",
		Affected: "Shared hosting",
		StartsAt: now.Add(-1 * time.Hour),
	}
	if err := seedNetworkIssue(ctx, pool, active); err != nil {
		return err
	}

	resolvedEnds := now.Add(-1 * time.Hour)
	resolved := networkIssueSpec{
		Title:    "Scheduled maintenance completed",
		Body:     "Scheduled maintenance has completed successfully.",
		Type:     "scheduled",
		Severity: "minor",
		Status:   "resolved",
		Affected: "All servers",
		StartsAt: now.Add(-3 * time.Hour),
		EndsAt:   &resolvedEnds,
	}
	return seedNetworkIssue(ctx, pool, resolved)
}

type networkIssueSpec struct {
	Title    string
	Body     string
	Type     string
	Severity string
	Status   string
	Affected string
	StartsAt time.Time
	EndsAt   *time.Time
}

func seedNetworkIssue(ctx context.Context, pool *pgxpool.Pool, spec networkIssueSpec) error {
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM network_issues WHERE title = $1`, spec.Title).Scan(&id); err == nil {
		return nil
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO network_issues (title, body, type, severity, status, affected, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		spec.Title, spec.Body, spec.Type, spec.Severity, spec.Status, spec.Affected,
		spec.StartsAt, spec.EndsAt)
	return err
}
