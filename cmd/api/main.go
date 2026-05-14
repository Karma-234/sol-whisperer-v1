package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.karma-234/sol-whisperer-v1/internal/auth"
	"github.karma-234/sol-whisperer-v1/internal/config"
	"github.karma-234/sol-whisperer-v1/internal/notification"
	"github.karma-234/sol-whisperer-v1/internal/pumpdev"
	"github.karma-234/sol-whisperer-v1/internal/rpc"
	"github.karma-234/sol-whisperer-v1/internal/snipe"
	"github.karma-234/sol-whisperer-v1/internal/store"
	"github.karma-234/sol-whisperer-v1/internal/volume"
	"github.karma-234/sol-whisperer-v1/internal/ws"
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

	tracker := volume.NewTracker(2.0, 5)
	pumpClient := pumpdev.NewClient(cfg.PumpDev.WSURL, logger)
	processor := volume.NewProcessor(tracker, sqliteStore, func(spike volume.SpikeResult) {
		payload, marshalErr := json.Marshal(fiber.Map{
			"type":             "volume_spike",
			"mint":             spike.Mint,
			"ratio":            spike.Ratio,
			"windowVolumeSOL":  spike.WindowVolumeSOL,
			"baselinePer5mSOL": spike.BaselinePer5mSOL,
			"uniqueWallets":    spike.UniqueWallets,
			"detectedAt":       spike.DetectedAt,
		})
		if marshalErr != nil {
			logger.Error().Err(marshalErr).Msg("failed to marshal spike websocket payload")
			return
		}

		hub.Broadcast(ws.Message{Priority: ws.PriorityP3Normal, Payload: payload})
		if notifyErr := telegramNotifier.Send(ctx, "", "Volume spike detected for mint: "+spike.Mint, notification.PriorityNormal); notifyErr != nil {
			logger.Warn().Err(notifyErr).Msg("telegram spike notification failed")
		}
	}, logger, volume.ProcessorConfig{MinSpikeEmitInterval: 20 * time.Second})

	pumpEvents, pumpErrs := pumpClient.Connect(ctx)
	if cfg.App.Env != "production" {
		logger.Info().Msg("development mode: enabling PumpDev mock event stream")
		pumpEvents = fanInEvents(ctx, pumpEvents, pumpClient.MockStream(ctx, 2*time.Second))
	}

	go func() {
		if runErr := processor.Run(ctx, pumpEvents, pumpErrs); runErr != nil {
			logger.Error().Err(runErr).Msg("volume processor stopped with error")
		}
	}()

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
			"jwt":             jwtManager.Algorithm(),
			"rpcTierAReady":   rpcManager.HasTier(rpc.TierA),
			"rpcTierBReady":   rpcManager.HasTier(rpc.TierB),
			"telegramEnabled": telegramNotifier.Enabled(),
			"jitoEnabled":     jitoService.Enabled(),
			"dryRun":          jitoService.DryRun(),
			"ws":              hub.Stats(),
		})
	})

	app.Get("/api/v1/spikes/recent", func(c *fiber.Ctx) error {
		recent, err := sqliteStore.GetRecentSpikeEvents(c.UserContext(), 100)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load spikes"})
		}
		return c.JSON(fiber.Map{"items": recent})
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
		if runErr != nil && !errors.Is(runErr, net.ErrClosed) {
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

func fanInEvents(ctx context.Context, channels ...<-chan pumpdev.Event) <-chan pumpdev.Event {
	out := make(chan pumpdev.Event)

	for _, ch := range channels {
		stream := ch
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-stream:
					if !ok {
						return
					}
					select {
					case <-ctx.Done():
						return
					case out <- ev:
					}
				}
			}
		}()
	}

	go func() {
		<-ctx.Done()
		close(out)
	}()

	return out
}
