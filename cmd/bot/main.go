package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"admin-bot/internal/config"
	"admin-bot/internal/domain"
	"admin-bot/internal/infra/accessstore"
	"admin-bot/internal/infra/apply"
	"admin-bot/internal/infra/oneedu"
	"admin-bot/internal/infra/scheduler"
	"admin-bot/internal/infra/sheets"
	"admin-bot/internal/infra/telegram"
	"admin-bot/internal/infra/templates"
	"admin-bot/internal/usecase"
	"admin-bot/internal/usecase/strategy"

	delivery "admin-bot/internal/delivery/telegram"
)

func main() {
	_ = godotenv.Load()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// Materialize inline Google credentials, if provided. Railway can't mount
	// files, so the service-account JSON is passed via GOOGLE_CREDENTIALS_JSON.
	// Write it to a guaranteed-writable temp path and point the config at it,
	// rather than the working directory (which may be root-owned/read-only).
	if creds := os.Getenv("GOOGLE_CREDENTIALS_JSON"); creds != "" {
		credPath := filepath.Join(os.TempDir(), "credentials.json")
		if err := os.WriteFile(credPath, []byte(creds), 0600); err != nil {
			logger.Error("failed to write Google credentials file", "path", credPath, "err", err)
		} else {
			cfg.GoogleCredentialsFile = credPath
			logger.Info("wrote inline Google credentials", "path", credPath)
		}
	}

	if len(cfg.AdminChatIDs) == 0 {
		logger.Warn("no ADMIN_CHAT_IDS/CHAT_IDS configured — bot will reject ALL commands (fail-closed)")
	}

	// Resolve the configured timezone once so date arithmetic (nextMonday) is
	// consistent with the cron schedule rather than the container's UTC clock.
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logger.Error("invalid timezone, using UTC", "timezone", cfg.Timezone, "err", err)
		loc = time.UTC
	}

	// Infrastructure
	eduClient := oneedu.NewClient(cfg.OneEduBaseURL, cfg.OneEduAccessToken, logger)
	tmplLoader := templates.NewFileLoader(cfg.TemplatesPath)

	var sheetsClient *sheets.Client
	if cfg.GoogleCredentialsFile != "" {
		sheetsClient, err = sheets.NewClient(cfg.GoogleCredentialsFile, logger)
		if err != nil {
			logger.Error("failed to create sheets client (continuing without it)", "err", err)
		} else {
			logger.Info("google sheets client initialized")
		}
	} else {
		logger.Warn("GOOGLE_CREDENTIALS_FILE not set — sheet updates will be unavailable")
	}

	strategies := []strategy.PiscineStrategy{
		strategy.NewGoStrategy(),
		strategy.NewJSStrategy(),
		strategy.NewAIStrategy(domain.PiscineAI_1),
		strategy.NewAIStrategy(domain.PiscineAI_2),
		strategy.NewAIStrategy(domain.PiscineAI_3),
		strategy.NewRustStrategy(),
	}

	// Lead client is optional: only build it when both the apply URL and token are
	// configured, otherwise /get_region_updates simply omits the lead line.
	var leadClient domain.LeadClient
	if cfg.ApplyBaseURL != "" && cfg.ApplyAccessToken != "" {
		leadClient = apply.NewClient(cfg.ApplyBaseURL, cfg.ApplyAccessToken, loc, logger)
		logger.Info("apply lead client initialized", "url", cfg.ApplyBaseURL)
	} else {
		logger.Warn("APPLY_BASE_URL/APPLY_ACCESS_TOKEN not set — region lead counts unavailable")
	}

	raidUC := usecase.NewRaidUseCase(eduClient, tmplLoader, strategies)
	updatesUC := usecase.NewUpdatesUseCase(eduClient, cfg.RegionEvents, leadClient)

	// Access store: fail-closed. If it can't load we exit rather than silently
	// running with an empty allowlist (which would deny every non-super-admin).
	store, err := accessstore.New(cfg.AccessStorePath)
	if err != nil {
		logger.Error("failed to load access store", "path", cfg.AccessStorePath, "err", err)
		os.Exit(1)
	}
	accessUC := usecase.NewAccessUseCase(store)

	// Pre-seed hand-configured admins (ADMIN_USER_IDS) as approved so an existing
	// allowlist keeps working after the switch to the request-based flow. The
	// per-id check makes this idempotent and safe to run every start: users with
	// an existing record (including a prior reject) are never overridden.
	for _, id := range cfg.AdminUserIDs {
		if _, ok := store.Get(id); ok {
			continue
		}
		if _, err := accessUC.Approve(id); err != nil {
			logger.Error("failed to pre-seed admin", "user_id", id, "err", err)
			os.Exit(1)
		}
		logger.Info("pre-seeded admin as approved", "user_id", id)
	}

	tgAdapter, err := telegram.NewAdapter(cfg.TelegramToken, logger)
	if err != nil {
		logger.Error("failed to create telegram adapter", "err", err)
		os.Exit(1)
	}

	handler := delivery.NewHandler(
		raidUC,
		updatesUC,
		accessUC,
		tgAdapter,
		sheetsClient,
		cfg.SheetIDs,
		cfg.SheetURLs,
		cfg.UniversalSheetID,
		cfg.AdminChatIDs,
		cfg.SuperAdminID,
		loc,
		logger,
	)
	delivery.RegisterHandlers(tgAdapter.Bot(), handler)

	sched := scheduler.NewCronScheduler(raidUC, tgAdapter, cfg.ChatIDs, cfg.Timezone, cfg.SheetURLs, logger)
	sched.DefenseCallback = handler.SendDefenseReminderWithKeyboard
	sched.Start()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("bot starting…", "timezone", cfg.Timezone, "chats", cfg.ChatIDs)

	// Run long-polling in a goroutine but track it with a WaitGroup so shutdown
	// waits for the poll loop (and any in-flight update handler) to unwind after
	// ctx is cancelled, rather than exiting the process mid-request.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tgAdapter.Start(ctx)
	}()

	<-ctx.Done()
	logger.Info("shutting down…")
	sched.Stop()
	wg.Wait()
	logger.Info("bye 👋")
}
