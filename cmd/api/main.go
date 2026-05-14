package main
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"sol-whisperer-v1/internal/auth"
	"sol-whisperer-v1/internal/config"
	"sol-whisperer-v1/internal/notification"
	"sol-whisperer-v1/internal/rpc"
	"sol-whisperer-v1/internal/snipe"
	"sol-whisperer-v1/internal/store"
	"sol-whisperer-v1/internal/ws"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := newLogger()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load config")
	}

	sqliteStore, err := store.NewSQLite(ctx, cfg.Database.SQLitePath, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to open sqlite")
	}
	defer func() {
		if closeErr := sqliteStore.Close(); closeErr != nil {
			logger.Error().Err(closeErr).Msg("failed to close sqlite")
		}
	}()

	jwtManager := auth.NewJWTManager(cfg.Security.JWTSecret, 24*time.Hour)
	rpcManager := rpc.NewTierManager(rpc.Config{
		TierARPC: cfg.RPC.TierARPC,
		TierAWS:  cfg.RPC.TierAWS,
		TierBRPC: cfg.RPC.TierBRPC,
		TierBWS:  cfg.RPC.TierBWS,
	})
	telegramNotifier := notification.NewTelegramNotifier(notification.Config{
		Enabled:       cfg.Telegram.Enabled,
		BotToken:      cfg.Telegram.BotToken,
		DefaultChatID: cfg.Telegram.DefaultChatID,
		HTTPClient:    &http.Client{Timeout: 8 * time.Second},
		Logger:        logger,
	})
	jitoService := snipe.NewJitoService(snipe.Config{
		Enabled:        cfg.Jito.Enabled,
		DryRun:         cfg.Sniping.DryRun,
		BlockEngineURL: cfg.Jito.BlockEngineURL,
		AuthKey:        cfg.Jito.AuthKey,
		DefaultTipSOL:  cfg.Jito.DefaultTipSOL,
		HTTPClient:     &http.Client{Timeout: 8 * time.Second},
		Logger:         logger,
	})

	hub := ws.NewHub(logger, 256)

	app := fiber.New(fiber.Config{
		AppName:               "sol-whisperer-v1",
		DisableStartupMessage: true,
	})

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"ts":     time.Now().UTC().Format(time.RFC3339),
		})
	})

	app.Get("/readyz", func(c *fiber.Ctx) error {
		if pingErr := sqliteStore.PingContext(c.Context()); pingErr != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "db-unavailable"})
		}
		return c.JSON(fiber.Map{"status": "ready"})
	})

	app.Get("/api/v1/bootstrap", func(c *fiber.Ctx) error {
		// This endpoint exists to validate base wiring for auth/rpc/notifier/service lifecycles.
		// Returning light-weight metadata helps frontend integration before feature-complete APIs exist.
		return c.JSON(fiber.Map{
			"jwt":            jwtManager.Algorithm(),
			"rpcTierAReady":  rpcManager.HasTier(rpc.TierA),
			"rpcTierBReady":  rpcManager.HasTier(rpc.TierB),
			"telegramEnabled": telegramNotifier.Enabled(),
			"jitoEnabled":     jitoService.Enabled(),
			"dryRun":          jitoService.DryRun(),
			"ws":              hub.Stats(),
		})
	})

	errCh := make(chan error, 1)
	go func() {
		addr := cfg.App.Host + ":" + cfg.App.Port
		logger.Info().Str("addr", addr).Msg("starting api server")
		errCh <- app.Listen(addr)
	}()

	select {
	case <-ctx.Done():
		logger.Info().Msg("shutdown signal received")
	case runErr := <-errCh:
		if runErr != nil && !errors.Is(runErr, fiber.ErrServerClosed) {
			logger.Fatal().Err(runErr).Msg("fiber server failed")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if stopErr := app.ShutdownWithContext(shutdownCtx); stopErr != nil {
		logger.Error().Err(stopErr).Msg("failed to shutdown fiber gracefully")
	}
}

func newLogger() zerolog.Logger {
	// Structured logs are required for high-throughput systems where string logs become
	// hard to parse and impossible to correlate quickly during incident response.
	zerolog.TimeFieldFormat = time.RFC3339Nano
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	return log.With().Str("service", "sol-whisperer-api").Logger()
}
