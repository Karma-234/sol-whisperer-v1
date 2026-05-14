package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.karma-234/sol-whisperer-v1/internal/auth"
	"github.karma-234/sol-whisperer-v1/internal/config"
	"github.karma-234/sol-whisperer-v1/internal/listener"
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

	telegramAuth := auth.NewTelegramAuth(cfg.Telegram.BotToken, 24*time.Hour)
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
	listenerRegistry := listener.NewRegistry()
	if activeListeners, listErr := sqliteStore.ListActiveListeners(ctx); listErr != nil {
		logger.Warn().Err(listErr).Msg("failed to preload active listeners")
	} else {
		for _, rec := range activeListeners {
			listenerRegistry.AddWatch(rec.UserID, rec.Mint)
		}
	}

	tracker := volume.NewTracker(2.0, 5)
	pumpClient := pumpdev.NewClient(cfg.PumpDev.WSURL, logger)
	processor := volume.NewProcessor(tracker, sqliteStore, func(spike volume.SpikeResult) {
		hasListener := listenerRegistry.HasWatchers(spike.Mint)
		broadcastPriority := ws.PriorityP3Normal
		notifyPriority := notification.PriorityNormal
		rpcTier := rpc.TierB
		if hasListener {
			notifyPriority = notification.PriorityHigh
			rpcTier = rpc.TierA
		}

		rpcEndpoint, rpcErr := rpcManager.NextRPC(rpcTier)
		if rpcErr != nil {
			rpcEndpoint = ""
		}

		payload, marshalErr := json.Marshal(fiber.Map{
			"type":             "volume_spike",
			"mint":             spike.Mint,
			"ratio":            spike.Ratio,
			"windowVolumeSOL":  spike.WindowVolumeSOL,
			"baselinePer5mSOL": spike.BaselinePer5mSOL,
			"uniqueWallets":    spike.UniqueWallets,
			"detectedAt":       spike.DetectedAt,
			"priority":         fmt.Sprintf("P%d", broadcastPriority),
			"tier":             string(rpcTier),
			"rpcEndpoint":      rpcEndpoint,
		})
		if marshalErr != nil {
			logger.Error().Err(marshalErr).Msg("failed to marshal spike websocket payload")
			return
		}

		hub.Broadcast(ws.Message{Priority: broadcastPriority, Payload: payload})
		if hasListener {
			personalPayload, perr := json.Marshal(fiber.Map{
				"type":             "personal_listener_spike",
				"mint":             spike.Mint,
				"ratio":            spike.Ratio,
				"windowVolumeSOL":  spike.WindowVolumeSOL,
				"baselinePer5mSOL": spike.BaselinePer5mSOL,
				"uniqueWallets":    spike.UniqueWallets,
				"detectedAt":       spike.DetectedAt,
				"priority":         "P1",
				"tier":             string(rpcTier),
				"rpcEndpoint":      rpcEndpoint,
			})
			if perr == nil {
				for _, userID := range listenerRegistry.UsersForMint(spike.Mint) {
					hub.EnqueueForUser(userID, ws.Message{Priority: ws.PriorityP1Critical, Personal: true, Payload: personalPayload, MaxRetries: 3})
				}
			}
		}
		if notifyErr := telegramNotifier.Send(ctx, "", "Volume spike detected for mint: "+spike.Mint, notifyPriority); notifyErr != nil {
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

	ws.RegisterRoutes(app, hub, logger, func(initData string) (string, error) {
		identity, err := telegramAuth.VerifyWebAppInitData(initData)
		if err != nil {
			return "", err
		}
		return identity.UserID, nil
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
			"authMode":        "telegram",
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

	app.Get("/api/v1/listeners/active", func(c *fiber.Ctx) error {
		identity, err := telegramIdentityFromRequest(c, telegramAuth)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "telegram authentication required"})
		}
		mints, qErr := sqliteStore.ListUserListenerMints(c.UserContext(), identity.UserID)
		if qErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load listeners"})
		}
		return c.JSON(fiber.Map{"userId": identity.UserID, "mints": mints})
	})

	app.Post("/api/v1/listeners/watch", func(c *fiber.Ctx) error {
		var req struct {
			Mint             string `json:"mint"`
			Symbol           string `json:"symbol"`
			AutoSnipeEnabled bool   `json:"autoSnipeEnabled"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		identity, authErr := telegramIdentityFromRequest(c, telegramAuth)
		if authErr != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "telegram authentication required"})
		}
		req.Mint = strings.TrimSpace(req.Mint)
		if req.Mint == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mint is required"})
		}

		rec := store.ListenerRecord{
			ID:               fmt.Sprintf("lis-%d", time.Now().UTC().UnixNano()),
			UserID:           identity.UserID,
			Mint:             req.Mint,
			Symbol:           strings.TrimSpace(req.Symbol),
			AutoSnipeEnabled: req.AutoSnipeEnabled,
		}
		if err := sqliteStore.UpsertListener(c.UserContext(), rec); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to persist listener"})
		}

		listenerRegistry.AddWatch(identity.UserID, req.Mint)
		tier := rpcManager.ChooseTierForToken(true)
		rpcEndpoint, rpcErr := rpcManager.NextRPC(tier)
		if rpcErr != nil {
			rpcEndpoint = ""
		}

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"status":      "watching",
			"userId":      identity.UserID,
			"mint":        req.Mint,
			"tier":        string(tier),
			"rpcEndpoint": rpcEndpoint,
		})
	})

	app.Delete("/api/v1/listeners/watch", func(c *fiber.Ctx) error {
		var req struct {
			Mint string `json:"mint"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		identity, authErr := telegramIdentityFromRequest(c, telegramAuth)
		if authErr != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "telegram authentication required"})
		}
		req.Mint = strings.TrimSpace(req.Mint)
		if req.Mint == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mint is required"})
		}

		if err := sqliteStore.DeleteListener(c.UserContext(), identity.UserID, req.Mint); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete listener"})
		}
		listenerRegistry.RemoveWatch(identity.UserID, req.Mint)
		return c.JSON(fiber.Map{"status": "removed", "userId": identity.UserID, "mint": req.Mint})
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

func telegramIdentityFromRequest(c *fiber.Ctx, verifier *auth.TelegramAuth) (auth.TelegramIdentity, error) {
	if verifier == nil {
		return auth.TelegramIdentity{}, errors.New("telegram verifier not configured")
	}
	initData := strings.TrimSpace(c.Get("X-Telegram-Init-Data"))
	if initData == "" {
		initData = strings.TrimSpace(c.Query("tgInitData"))
	}
	if initData == "" {
		return auth.TelegramIdentity{}, errors.New("telegram init data missing")
	}
	return verifier.VerifyWebAppInitData(initData)
}
