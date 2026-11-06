// WHCMS worker: asynq server + periodic scheduler. It builds the same
// service graph as cmd/api via composition.Build (a separate OS process, so
// it cannot reuse cmd/api's Go values) and registers every job/cron handler
// via worker.RegisterHandlers + worker.Schedules (docs/WIRING.md).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tsdlamongan/whcms/backend/internal/composition"
	"github.com/tsdlamongan/whcms/backend/internal/installer"
	"github.com/tsdlamongan/whcms/backend/internal/platform/config"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/platform/lock"
	"github.com/tsdlamongan/whcms/backend/internal/platform/logger"
	"github.com/tsdlamongan/whcms/backend/internal/platform/redisx"
	"github.com/tsdlamongan/whcms/backend/internal/platform/storage"
	"github.com/tsdlamongan/whcms/backend/internal/worker"

	"github.com/hibiken/asynq"

	"github.com/tsdlamongan/whcms/backend/internal/platform/queue"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker: fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Best-effort: pick up whatever env file the API's installation wizard
	// may have already written (docs/CONTRACTS.md §15). A real env var
	// always wins; a missing file is not an error.
	_ = config.ApplyEnvFile(installer.EnvFilePath())

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.AppEnv)
	slog.SetDefault(log)
	ctx := context.Background()

	// Connect shared infrastructure (this process's own connections).
	database, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	rdb, err := redisx.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	s3, err := storage.New(ctx, storage.Options{
		Endpoint:  cfg.RustFSEndpoint,
		AccessKey: cfg.RustFSAccessKey,
		SecretKey: cfg.RustFSSecretKey,
		Bucket:    cfg.RustFSBucket,
		UseSSL:    cfg.RustFSUseSSL,
	})
	if err != nil {
		return err
	}

	// --- Composition root: build every repo/adapter/service ----------------
	app, err := composition.Build(ctx, cfg, database, rdb, s3, log)
	if err != nil {
		return err
	}

	redisOpt := queue.RedisOpt(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)

	// --- asynq server ------------------------------------------------------
	srv := queue.NewServer(redisOpt, cfg.WorkerConcurrency, log)
	mux := asynq.NewServeMux()
	worker.RegisterHandlers(mux, worker.Deps{
		Orders:        app.Orders,
		Provisioning:  app.Provisioning,
		Domains:       app.Domains,
		Billing:       app.Billing,
		Payments:      app.Payments,
		Notifications: app.Notifications,
		AdminOps:      app.AdminOps,
		Notifier:      app.Notifications,
		Locker:        lock.New(rdb),
		Log:           log,
	})

	// --- periodic scheduler --------------------------------------------
	scheduler := queue.NewScheduler(redisOpt, log)
	schedules := worker.Schedules()
	for _, sched := range schedules {
		if _, err := scheduler.Register(sched.Spec, asynq.NewTask(sched.Type, nil)); err != nil {
			return err
		}
	}

	if err := srv.Start(mux); err != nil {
		return err
	}
	if err := scheduler.Start(); err != nil {
		srv.Shutdown()
		return err
	}

	// --- domain sniper supervisor ------------------------------------------
	// Not an asynq task: a hunt polls every few hundred milliseconds for days,
	// which asynq's 30-minute default timeout, 5s scheduling granularity and
	// shared worker slots cannot serve. State lives in Postgres under an SQL
	// lease, so a restart (or a second worker) just resumes.
	sniperCtx, stopSniper := context.WithCancel(ctx)
	sniperDone := make(chan struct{})
	go func() {
		defer close(sniperDone)
		app.SnipeSupervisor.Run(sniperCtx)
	}()
	log.Info("worker running", "concurrency", cfg.WorkerConcurrency, "crons", len(schedules))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Info("worker shutting down")
	stopSniper()
	<-sniperDone
	scheduler.Shutdown()
	srv.Stop()
	srv.Shutdown()
	return nil
}
