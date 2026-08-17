// Package composition wires every repository, adapter and service into one
// *App, shared by cmd/api and cmd/worker (docs/WIRING.md §1). See
// forwarders.go for the late-bound handles that break the cross-module
// service dependency cycle described in docs/WIRING.md §0.
package composition

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/integration/cpanel"
	"github.com/tsdlamongan/whcms/backend/internal/integration/directadmin"
	"github.com/tsdlamongan/whcms/backend/internal/integration/duitku"
	"github.com/tsdlamongan/whcms/backend/internal/integration/manual"
	"github.com/tsdlamongan/whcms/backend/internal/integration/rdash"
	"github.com/tsdlamongan/whcms/backend/internal/integration/turnstile"
	"github.com/tsdlamongan/whcms/backend/internal/modules/adminops"
	"github.com/tsdlamongan/whcms/backend/internal/modules/announcements"
	"github.com/tsdlamongan/whcms/backend/internal/modules/auth"
	"github.com/tsdlamongan/whcms/backend/internal/modules/billing"
	"github.com/tsdlamongan/whcms/backend/internal/modules/catalog"
	"github.com/tsdlamongan/whcms/backend/internal/modules/clients"
	"github.com/tsdlamongan/whcms/backend/internal/modules/contact"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/modules/install"
	"github.com/tsdlamongan/whcms/backend/internal/modules/knowledgebase"
	"github.com/tsdlamongan/whcms/backend/internal/modules/networkstatus"
	"github.com/tsdlamongan/whcms/backend/internal/modules/notifications"
	"github.com/tsdlamongan/whcms/backend/internal/modules/orders"
	"github.com/tsdlamongan/whcms/backend/internal/modules/payments"
	"github.com/tsdlamongan/whcms/backend/internal/modules/provisioning"
	"github.com/tsdlamongan/whcms/backend/internal/modules/tickets"
	"github.com/tsdlamongan/whcms/backend/internal/platform/cache"
	"github.com/tsdlamongan/whcms/backend/internal/platform/clock"
	"github.com/tsdlamongan/whcms/backend/internal/platform/config"
	"github.com/tsdlamongan/whcms/backend/internal/platform/crypto"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/platform/mailer"
	"github.com/tsdlamongan/whcms/backend/internal/platform/pdf"
	"github.com/tsdlamongan/whcms/backend/internal/platform/presence"
	"github.com/tsdlamongan/whcms/backend/internal/platform/queue"
	"github.com/tsdlamongan/whcms/backend/internal/platform/ratelimit"
	"github.com/tsdlamongan/whcms/backend/internal/platform/storage"
	"github.com/tsdlamongan/whcms/backend/internal/platform/tokenstore"
	"github.com/tsdlamongan/whcms/backend/internal/platform/totpguard"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/repository"
	"github.com/tsdlamongan/whcms/backend/internal/service/authtoken"
	"github.com/tsdlamongan/whcms/backend/internal/service/captcha"
	settingssvc "github.com/tsdlamongan/whcms/backend/internal/service/settings"
)

// App holds every constructed service (plus a few infra handles callers
// need directly), shared by cmd/api and cmd/worker.
type App struct {
	Auth          *auth.Service
	Clients       *clients.Service
	Catalog       *catalog.Service
	Orders        *orders.Service
	Billing       *billing.Service
	Payments      *payments.Service
	Provisioning  *provisioning.Service
	Domains       *domains.Service
	Tickets       *tickets.Service
	Notifications *notifications.Service
	AdminOps      *adminops.Service
	Announcements *announcements.Service
	Knowledgebase *knowledgebase.Service
	NetworkStatus *networkstatus.Service
	Contact       *contact.Service
	Settings      *settingssvc.Service
	Install       *install.Service
	Captcha       *captcha.Guard

	AuthUsersRepo ports.UserRepo     // needed by cmd/api for the PermissionSource
	Tokens        *authtoken.Manager // needed by cmd/api for the auth middleware's AccessParser

	Enqueuer *queue.Enqueuer // shared asynq enqueuer (cmd/worker scheduler reuses the same redis opts)
}

// Build constructs every repository, adapter and service and returns the
// assembled *App. Each of cmd/api and cmd/worker calls this once against its
// own DB/Redis/S3 connections (separate OS processes cannot share Go values).
func Build(ctx context.Context, cfg config.Config, database *db.DB, rdb *redis.Client, s3 *storage.S3, log *slog.Logger) (*App, error) {
	clk := clock.New()
	txManager := db.NewTxManager(database)

	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	hasher := crypto.NewPasswordHasher()

	// 1. Repositories (any order; none depend on a service).
	authUsersRepo := auth.NewUserRepo(database)
	clientsRepo := clients.NewRepo(database)
	catalogRepo := catalog.NewRepo(database)
	ordersRepo := orders.NewRepo(database)
	billingRepo := billing.NewRepo(database)
	paymentsRepo := payments.NewRepo(database)
	provisioningRepo := provisioning.NewRepo(database)
	domainsRepo := domains.NewRepo(database)
	domainsRegistrarRepo := domains.NewRegistrarRepo(database)
	tldPricingRepo := domains.NewTLDPricingRepo(database)
	premiumPricingRepo := domains.NewPremiumDomainPricingRepo(database)
	premiumLengthPricingRepo := domains.NewPremiumLengthPricingRepo(database)
	domainAddonRepo := domains.NewDomainAddonRepo(database)
	ticketsRepo := tickets.NewRepo(database)
	templateRepo := notifications.NewTemplateRepo(database)
	adminopsRepo := adminops.NewRepo(database)

	settingsRepo := repository.NewSettingsRepo(database)
	auditRepo := repository.NewAuditRepo(database)
	auditLogger := repository.NewAuditLogger(auditRepo, log)
	emailLogRepo := repository.NewEmailLogRepo(database)
	integrationLogRepo := repository.NewIntegrationLogRepo(database)
	integrationLogger := repository.NewIntegrationLogger(integrationLogRepo, log)

	// 2. Forwarders (zero value; bound to real services in step 5).
	icFwd := &invoiceCreatorFwd{}
	saFwd := &serviceActivatorFwd{}
	srFwd := &serviceRenewerFwd{}
	drFwd := &domainRenewerFwd{}
	paFwd := &paymentApplierFwd{}
	pipFwd := &paidInvoiceProcessorFwd{}
	creditFwd := &creditServiceFwd{}
	notifyFwd := &notificationSenderFwd{}

	// 3. Adapters.
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// resolveDuitkuCredentials/resolveRDashCredentials let an admin rotate the
	// Duitku/RDash merchant code, API key and base URL (a "custom endpoint"
	// override for pointing at the mock vs the real sandbox/production API)
	// live (encrypted at rest for secrets) with no process restart:
	// consulted fresh on every request (no caching - the same "cheap DB read
	// in front of a much slower external call" tradeoff settings reads
	// already make elsewhere), falling back to the static env-sourced values
	// on any read/decrypt error.
	resolveDuitkuCredentials := func(ctx context.Context) (duitku.Credentials, error) {
		merchantCode, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuMerchantCode, cfg.DuitkuMerchantCode)
		if err != nil {
			return duitku.Credentials{}, err
		}
		mode, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuMode, cfg.DuitkuEnv)
		if err != nil {
			return duitku.Credentials{}, err
		}
		settingBaseURL, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuBaseURL, "")
		if err != nil {
			return duitku.Credentials{}, err
		}
		baseURL := resolveDuitkuBaseURL(settingBaseURL, cfg.DuitkuBaseURLExplicit, mode)
		enc, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuAPIKeyEnc, "")
		if err != nil {
			return duitku.Credentials{}, err
		}
		apiKey := cfg.DuitkuAPIKey
		if enc != "" {
			dec, err := encryptor.Decrypt(enc)
			if err != nil {
				return duitku.Credentials{}, err
			}
			apiKey = dec
		}
		return duitku.Credentials{MerchantCode: merchantCode, APIKey: apiKey, BaseURL: baseURL}, nil
	}
	duitkuAdapter := duitku.New(duitku.Config{
		MerchantCode: cfg.DuitkuMerchantCode,
		APIKey:       cfg.DuitkuAPIKey,
		BaseURL:      cfg.DuitkuBaseURL,
		CallbackURL:  cfg.AppBaseURL + "/api/v1/webhooks/duitku",
		ReturnURL:    cfg.FrontendURL + "/payments/return",
	}, resolveDuitkuCredentials, nil, integrationLogger, clk)
	// resolveManualGatewayConfig re-reads the bank-transfer config fresh on
	// every call, same live/no-restart pattern as Duitku/RDash - nothing
	// here is a secret, so no encryptor/env-fallback needed.
	resolveManualGatewayConfig := func(ctx context.Context) (manual.Config, error) {
		var mgc adminops.ManualGatewayConfig
		if err := settingsRepo.GetJSON(ctx, adminops.SettingManualGatewayConfig, &mgc); err != nil {
			return manual.Config{}, err
		}
		return manual.Config{Enabled: mgc.Enabled, Accounts: mgc.Accounts, Instructions: mgc.Instructions}, nil
	}
	manualAdapter := manual.New(resolveManualGatewayConfig)
	cpanelAdapter := cpanel.New(cpanel.Config{TLSInsecureSkipVerify: cfg.PanelTLSInsecureSkipVerify}, httpClient, integrationLogger, clk)
	directadminAdapter := directadmin.New(directadmin.Config{TLSInsecureSkipVerify: cfg.PanelTLSInsecureSkipVerify}, httpClient, integrationLogger, clk)
	resolveRDashCredentials := func(ctx context.Context) (rdash.Credentials, error) {
		reg, err := domainsRegistrarRepo.GetByName(ctx, "rdash")
		if err != nil {
			return rdash.Credentials{}, err
		}
		resellerID := reg.ResellerID
		if resellerID == "" {
			resellerID = cfg.RDashResellerID
		}
		apiKey := cfg.RDashAPIKey
		if reg.APIKeyEnc != "" {
			dec, err := encryptor.Decrypt(reg.APIKeyEnc)
			if err != nil {
				return rdash.Credentials{}, err
			}
			apiKey = dec
		}
		baseURL := reg.BaseURL
		if baseURL == "" {
			baseURL = cfg.RDashBaseURL
		}
		return rdash.Credentials{BaseURL: baseURL, ResellerID: resellerID, APIKey: apiKey}, nil
	}
	rdashAdapter := rdash.New(rdash.Config{
		BaseURL:    cfg.RDashBaseURL,
		ResellerID: cfg.RDashResellerID,
		APIKey:     cfg.RDashAPIKey,
	}, resolveRDashCredentials, nil, integrationLogger, clk)
	turnstileAdapter := turnstile.New(turnstile.Config{
		SecretKey: cfg.TurnstileSecretKey,
		VerifyURL: cfg.TurnstileVerifyURL,
	}, httpClient, integrationLogger, clk)
	captchaGuard := captcha.New(turnstileAdapter, settingsRepo)

	mailerImpl, err := mailer.New(cfg, log)
	if err != nil {
		return nil, err
	}

	redisOpt := queue.RedisOpt(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	enqueuer := queue.NewEnqueuer(queue.NewClient(redisOpt))
	jobInspector := queue.NewInspector(redisOpt)

	// 4. Services (any order; forwarders stand in for the cyclic deps).
	tokens := authtoken.New(cfg.JWTSecret, rdb, clk)
	presenceSvc := presence.New(rdb, 5*time.Minute) // "Staff Online" widget
	authSvc := auth.New(auth.Deps{
		Users:       authUsersRepo,
		Clients:     clientsRepo,
		Tx:          txManager,
		Hasher:      hasher,
		Tokens:      tokens,
		OneTime:     tokenstore.New(rdb),
		Notify:      notifyFwd,
		Limiter:     ratelimit.New(rdb),
		Audit:       auditLogger,
		Clock:       clk,
		Encryptor:   encryptor,
		TOTPGuard:   totpguard.New(rdb),
		Presence:    presenceSvc,
		Captcha:     captchaGuard,
		FrontendURL: cfg.FrontendURL,
	})

	clientsSvc := clients.New(clients.Deps{
		Tx:        txManager,
		Clients:   clientsRepo,
		Credits:   clientsRepo,
		Contacts:  clientsRepo,
		Search:    clientsRepo,
		Aggregate: clientsRepo,
		Export:    clientsRepo,
		Users:     authUsersRepo,
		Invoices:  icFwd,
		Settings:  settingsRepo,
		Hasher:    hasher,
		Audit:     auditLogger,
		Clock:     clk,
	})
	creditFwd.inner = clientsSvc

	catalogSvc := catalog.New(catalog.Deps{
		Products: catalogRepo,
		Options:  catalogRepo,
		Specs:    catalogRepo,
		Coupons:  catalogRepo.CouponRepo(),
		Tx:       txManager,
		Cache:    cache.New(rdb),
		Audit:    auditLogger,
		Clock:    clk,
	})

	ordersSvc := orders.New(orders.Deps{
		Tx:                   txManager,
		Orders:               ordersRepo,
		Products:             catalogRepo,
		Stock:                ordersRepo,
		Coupons:              catalogRepo.CouponRepo(),
		Clients:              clientsRepo,
		Users:                authUsersRepo,
		Services:             provisioningRepo,
		Domains:              domainsRepo,
		Registrars:           domainsRegistrarRepo,
		TLDPricing:           tldPricingRepo,
		PremiumPricing:       premiumPricingRepo,
		PremiumLengthPricing: premiumLengthPricingRepo,
		DomainAddons:         domainAddonRepo,
		Registrar:            rdashAdapter,
		Invoices:             icFwd,
		InvoiceSt:            billingRepo,
		Payments:             paFwd,
		Settings:             settingsRepo,
		Enqueuer:             enqueuer,
		Notifier:             notifyFwd,
		Encryptor:            encryptor,
		Audit:                auditLogger,
		Clock:                clk,
		Captcha:              captchaGuard,
	})
	saFwd.inner = ordersSvc

	billingSvc := billing.New(billing.Deps{
		Tx:            txManager,
		Invoices:      billingRepo,
		Transactions:  paymentsRepo,
		Clients:       clientsRepo,
		Users:         authUsersRepo,
		Services:      provisioningRepo,
		Domains:       domainsRepo,
		Coupons:       catalogRepo.CouponRepo(),
		Settings:      settingsRepo,
		Credit:        creditFwd,
		Payments:      paFwd,
		Activator:     saFwd,
		Renewer:       srFwd,
		DomainRenewer: drFwd,
		Notifier:      notifyFwd,
		PDF:           pdf.New(),
		Storage:       s3,
		Enqueuer:      enqueuer,
		Audit:         auditLogger,
		Clock:         clk,
		FrontendURL:   cfg.FrontendURL,
	})
	icFwd.inner = billingSvc
	pipFwd.inner = billingSvc

	paymentsSvc := payments.New(payments.Deps{
		Tx:           txManager,
		Invoices:     billingRepo,
		Transactions: paymentsRepo,
		Clients:      clientsRepo,
		Users:        authUsersRepo,
		Gateways: map[string]ports.PaymentGateway{
			string(domain.GatewayDuitku): duitkuAdapter,
			string(domain.GatewayManual): manualAdapter,
		},
		GatewayOrder:   []string{string(domain.GatewayDuitku), string(domain.GatewayManual)},
		DefaultGateway: string(domain.GatewayDuitku),
		Processor:      pipFwd,
		Credits:        creditFwd,
		Cache:          cache.New(rdb),
		Clock:          clk,
		Audit:          auditLogger,
		IntLog:         integrationLogger,
		Log:            log,
		Settings:       settingsRepo,
		AppBaseURL:     cfg.AppBaseURL,
		FrontendURL:    cfg.FrontendURL,
	})
	paFwd.inner = paymentsSvc

	provisioningSvc := provisioning.New(provisioning.Deps{
		Services: provisioningRepo,
		Servers:  provisioningRepo,
		Products: catalogRepo,
		Clients:  clientsRepo,
		Users:    authUsersRepo,
		Invoices: billingRepo,
		Modules: map[string]ports.ServerModule{
			"cpanel":      cpanelAdapter,
			"directadmin": directadminAdapter,
		},
		Crypt:    encryptor,
		Queue:    enqueuer,
		Notify:   notifyFwd,
		Billing:  icFwd,
		Credit:   creditFwd,
		Settings: settingsRepo,
		Tx:       txManager,
		Audit:    auditLogger,
		Clock:    clk,

		CancellationRequests: provisioningRepo.CancellationRequests(),
	})
	srFwd.inner = provisioningSvc

	domainsSvc := domains.New(domains.Deps{
		Tx:                     txManager,
		Domains:                domainsRepo,
		Registrars:             domainsRegistrarRepo,
		TLDPricing:             tldPricingRepo,
		PremiumPricing:         premiumPricingRepo,
		PremiumLengthPricing:   premiumLengthPricingRepo,
		DomainAddons:           domainAddonRepo,
		Clients:                clientsRepo,
		Users:                  authUsersRepo,
		Settings:               settingsRepo,
		Registrar:              rdashAdapter,
		Enqueuer:               enqueuer,
		Invoices:               icFwd,
		RenewalCheck:           billingRepo,
		Notifier:               notifyFwd,
		Encryptor:              encryptor,
		Audit:                  auditLogger,
		Clock:                  clk,
		Log:                    log,
		RegistrarAPIKeyPresent: cfg.RDashAPIKey != "",
		AllowPrivateBaseURL:    !cfg.IsProduction(),
	})
	drFwd.inner = domainsSvc

	ticketsSvc := tickets.New(tickets.Deps{
		Tx:          txManager,
		Tickets:     ticketsRepo,
		Search:      ticketsRepo,
		Clients:     clientsRepo,
		Users:       authUsersRepo,
		Storage:     s3,
		Settings:    settingsRepo,
		Notifier:    notifyFwd,
		Audit:       auditLogger,
		Clock:       clk,
		FrontendURL: cfg.FrontendURL,
	})

	notificationsSvc := notifications.New(notifications.Deps{
		Templates:            templateRepo,
		Logs:                 emailLogRepo,
		Users:                authUsersRepo,
		Clients:              clientsRepo,
		Settings:             settingsRepo,
		Storage:              s3,
		Mailer:               mailerImpl,
		Enqueuer:             enqueuer,
		Audit:                auditLogger,
		Clock:                clk,
		IntegrationLog:       integrationLogger,
		FrontendURL:          cfg.FrontendURL,
		AdminAlertEmail:      cfg.AdminAlertEmail,
		AdminAlertWebhookURL: cfg.AdminAlertWebhookURL,
	})
	notifyFwd.inner = notificationsSvc

	adminopsSvc := adminops.New(adminops.Deps{
		Dashboard: adminopsRepo,
		Logs:      adminopsRepo,
		Staff:     adminopsRepo,
		Users:     authUsersRepo,
		Audit:     auditRepo,
		EmailLogs: emailLogRepo,
		Settings:  settingsRepo,
		Cache:     cache.New(rdb),
		Hasher:    hasher,
		AuditLog:  auditLogger,
		Clock:     clk,
		Presence:  presenceSvc,
		Jobs:      jobInspector,
		Secrets: adminops.GatewaySecrets{
			DuitkuMerchantCode: cfg.DuitkuMerchantCode,
			DuitkuMode:         cfg.DuitkuEnv,
			DuitkuBaseURL:      cfg.DuitkuBaseURL,
			DuitkuAPIKeySet:    cfg.DuitkuAPIKey != "",
		},
		Encryptor: encryptor,
	})

	settingsSvc := settingssvc.New(settingsRepo, txManager, auditLogger)

	installSvc := install.New(install.Deps{
		Users:    authUsersRepo,
		Hasher:   hasher,
		AuditLog: auditLogger,
		Settings: settingsSvc,
		Clock:    clk,
	})

	// Portal feature modules (announcements, knowledgebase, network
	// status, contact). Contact delegates to ticketsSvc, so it is
	// built after ticketsSvc above.
	announcementsRepo := announcements.NewRepo(database)
	announcementsSvc := announcements.New(announcements.Deps{
		Repo:  announcementsRepo,
		Audit: auditLogger,
		Clock: clk,
	})

	kbRepo := knowledgebase.NewRepo(database)
	kbSvc := knowledgebase.New(knowledgebase.Deps{
		Repo:  kbRepo,
		Tx:    txManager,
		Audit: auditLogger,
		Clock: clk,
	})

	networkRepo := networkstatus.NewRepo(database)
	networkSvc := networkstatus.New(networkstatus.Deps{
		Repo:  networkRepo,
		Audit: auditLogger,
		Clock: clk,
	})

	contactSvc := contact.New(contact.Deps{
		Tickets: ticketsSvc,
		Clock:   clk,
	})

	return &App{
		Auth:          authSvc,
		Clients:       clientsSvc,
		Catalog:       catalogSvc,
		Orders:        ordersSvc,
		Billing:       billingSvc,
		Payments:      paymentsSvc,
		Provisioning:  provisioningSvc,
		Domains:       domainsSvc,
		Tickets:       ticketsSvc,
		Notifications: notificationsSvc,
		AdminOps:      adminopsSvc,
		Announcements: announcementsSvc,
		Knowledgebase: kbSvc,
		NetworkStatus: networkSvc,
		Contact:       contactSvc,
		Settings:      settingsSvc,
		Install:       installSvc,
		Captcha:       captchaGuard,
		AuthUsersRepo: authUsersRepo,
		Tokens:        tokens,
		Enqueuer:      enqueuer,
	}, nil
}

// resolveDuitkuBaseURL applies the live Duitku base URL precedence:
//  1. settingBaseURL - the admin-configured gateway.duitku.base_url setting
//     (a "custom endpoint" override, e.g. pointing at a mock server) - wins
//     unconditionally when set.
//  2. envExplicitBaseURL - the raw DUITKU_BASE_URL env var, when an operator
//     explicitly pinned it (config.Config.DuitkuBaseURLExplicit; e.g. the
//     E2E/dev stack pins it at the local mockserver). This is a deliberate
//     "always use this endpoint" pin and must win over the live mode setting
//     below, or every dev/E2E run would suddenly start calling real Duitku.
//  3. Otherwise, derive live from mode (sandbox/production) - this is what
//     lets an admin flip Sandbox<->Production via the settings UI take effect
//     immediately, with no restart, as long as neither override above is set.
func resolveDuitkuBaseURL(settingBaseURL, envExplicitBaseURL, mode string) string {
	if settingBaseURL != "" {
		return settingBaseURL
	}
	if envExplicitBaseURL != "" {
		return envExplicitBaseURL
	}
	if mode == "production" {
		return "https://passport.duitku.com"
	}
	return "https://sandbox.duitku.com"
}
