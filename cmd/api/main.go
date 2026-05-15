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
	"github.karma-234/sol-whisperer-v1/internal/pumpportal"
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
	if cfg.Telegram.WebAppURL != "" {
		setupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		if menuErr := telegramNotifier.ConfigureWebAppMenu(setupCtx, cfg.Telegram.WebAppURL, cfg.Telegram.WebAppButtonText); menuErr != nil {
			logger.Warn().Err(menuErr).Msg("failed to configure telegram web app menu button")
		} else {
			logger.Info().Str("webAppURL", cfg.Telegram.WebAppURL).Msg("telegram web app menu button configured")
		}
		cancel()
	}
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
	portalTradeTracker := pumpportal.NewTradeTracker(cfg.PumpPortal.WSURL, cfg.PumpPortal.APIKey, logger)
	if activeListeners, listErr := sqliteStore.ListActiveListeners(ctx); listErr != nil {
		logger.Warn().Err(listErr).Msg("failed to preload active listeners")
	} else {
		for _, rec := range activeListeners {
			listenerRegistry.AddWatch(rec.UserID, rec.Mint)
			portalTradeTracker.AddWatch(rec.Mint)
		}
	}

	tracker := volume.NewTracker(12.0, 5)
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
			"name":             spike.Name,
			"symbol":           spike.Symbol,
			"ratio":            spike.Ratio,
			"windowVolumeSOL":  spike.WindowVolumeSOL,
			"baselinePer5mSOL": spike.BaselinePer5mSOL,
			"marketCapSOL":     spike.MarketCapSOL,
			"uniqueWallets":    spike.UniqueWallets,
			"tokenCreatedAt":   spike.TokenCreatedAt,
			"tokenAgeSeconds":  spike.TokenAgeSeconds,
			"floorConfidence":  spike.FloorConfidence,
			"entryScore":       spike.EntryScore,
			"entryGrade":       spike.EntryGrade,
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
				"name":             spike.Name,
				"symbol":           spike.Symbol,
				"ratio":            spike.Ratio,
				"windowVolumeSOL":  spike.WindowVolumeSOL,
				"baselinePer5mSOL": spike.BaselinePer5mSOL,
				"marketCapSOL":     spike.MarketCapSOL,
				"uniqueWallets":    spike.UniqueWallets,
				"tokenCreatedAt":   spike.TokenCreatedAt,
				"tokenAgeSeconds":  spike.TokenAgeSeconds,
				"floorConfidence":  spike.FloorConfidence,
				"entryScore":       spike.EntryScore,
				"entryGrade":       spike.EntryGrade,
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
		tokenLabel := spike.Mint
		if spike.Name != "" && spike.Symbol != "" {
			tokenLabel = spike.Name + " / " + spike.Symbol
		} else if spike.Symbol != "" {
			tokenLabel = spike.Symbol
		} else if spike.Name != "" {
			tokenLabel = spike.Name
		}
		notificationMessage := formatSpikeAlert(tokenLabel, spike, rpcTier, hasListener)
		if hasListener {
			for _, userID := range listenerRegistry.UsersForMint(spike.Mint) {
				if notifyErr := telegramNotifier.Send(ctx, userID, notificationMessage, notifyPriority); notifyErr != nil {
					logger.Warn().Err(notifyErr).Str("userId", userID).Msg("telegram personal spike notification failed")
				}
			}
		} else if notifyErr := telegramNotifier.Send(ctx, "", notificationMessage, notifyPriority); notifyErr != nil {
			logger.Warn().Err(notifyErr).Msg("telegram spike notification failed")
		}
	}, logger, volume.ProcessorConfig{MinSpikeEmitInterval: 20 * time.Second, TxDedupeWindow: 5 * time.Minute})

	rpcWSClient := rpc.NewWSClient(rpcManager, logger)
	portalBuffer := pumpportal.NewRecentBuffer(80)
	portalClient := pumpportal.NewClient(cfg.PumpPortal.WSURL, cfg.PumpPortal.APIKey, cfg.PumpPortal.MigrationCapturePath, logger)
	portalEnricher := pumpportal.NewDexScreenerEnricher(&http.Client{Timeout: 4 * time.Second}, logger)
	portalIdentityCache := make(map[string]pumpportal.Event)

	pumpEvents, pumpErrs := pumpClient.Connect(ctx)
	rpcEvents, rpcErrs := rpcWSClient.Connect(ctx)

	go func() {
		if runErr := processor.RunDual(ctx, pumpEvents, pumpErrs, rpcEvents, rpcErrs); runErr != nil {
			logger.Error().Err(runErr).Msg("volume processor stopped with error")
		}
	}()

	if portalClient.Enabled() {
		portalEvents, portalErrs := portalClient.Connect(ctx)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err, ok := <-portalErrs:
					if !ok {
						portalErrs = nil
						continue
					}
					if err != nil {
						logger.Warn().Err(err).Msg("pumpportal stream error")
					}
				case ev, ok := <-portalEvents:
					if !ok {
						portalEvents = nil
						continue
					}
					if cached, ok := portalIdentityCache[ev.Mint]; ok {
						if strings.TrimSpace(ev.Name) == "" {
							ev.Name = cached.Name
						}
						if strings.TrimSpace(ev.Symbol) == "" {
							ev.Symbol = cached.Symbol
						}
						if strings.TrimSpace(ev.URI) == "" {
							ev.URI = cached.URI
						}
					}
					if ev.Stream == pumpportal.StreamMigrated {
						enrichCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
						ev = portalEnricher.Enrich(enrichCtx, ev)
						cancel()
					}
					if strings.TrimSpace(ev.Name) != "" || strings.TrimSpace(ev.Symbol) != "" || strings.TrimSpace(ev.URI) != "" {
						portalIdentityCache[ev.Mint] = pumpportal.Event{
							Mint:   ev.Mint,
							Name:   ev.Name,
							Symbol: ev.Symbol,
							URI:    ev.URI,
						}
					}
					portalBuffer.Add(ev)
					eventType := "portal_new_token"
					if ev.Stream == pumpportal.StreamMigrated {
						eventType = "portal_migration"
					}
					payload, marshalErr := json.Marshal(fiber.Map{
						"type":          eventType,
						"stream":        ev.Stream,
						"mint":          ev.Mint,
						"name":          ev.Name,
						"symbol":        ev.Symbol,
						"uri":           ev.URI,
						"pool":          ev.Pool,
						"isMayhemMode":  ev.IsMayhemMode,
						"txType":        ev.TxType,
						"signature":     ev.Signature,
						"marketCapSOL":  ev.MarketCapSOL,
						"initialBuySOL": ev.InitialBuySOL,
						"dexId":         ev.DexID,
						"pairAddress":   ev.PairAddress,
						"priceUsd":      ev.PriceUSD,
						"priceNative":   ev.PriceNative,
						"marketCapUsd":  ev.MarketCapUSD,
						"liquidityUsd":  ev.LiquidityUSD,
						"fdv":           ev.FDV,
						"volume5mUsd":   ev.Volume5mUSD,
						"volume1hUsd":   ev.Volume1hUSD,
						"buys5m":        ev.Buys5m,
						"sells5m":       ev.Sells5m,
						"pairCreatedAt": ev.PairCreatedAt,
						"imageUrl":      ev.ImageURL,
						"websiteUrl":    ev.WebsiteURL,
						"socialHandle":  ev.SocialHandle,
						"detectedAt":    ev.Timestamp,
						"rawPayload":    ev.RawPayload,
					})
					if marshalErr == nil {
						hub.Broadcast(ws.Message{Priority: ws.PriorityP2High, Payload: payload})
					}
				}

				if portalEvents == nil && portalErrs == nil {
					return
				}
			}
		}()
	} else {
		logger.Info().Msg("pumpportal feed disabled: api key not configured")
	}

	if portalTradeTracker.Enabled() {
		tradeUpdates, tradeErrs := portalTradeTracker.Connect(ctx)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err, ok := <-tradeErrs:
					if !ok {
						tradeErrs = nil
						continue
					}
					if err != nil {
						logger.Warn().Err(err).Msg("pumpportal trade tracker error")
					}
				case update, ok := <-tradeUpdates:
					if !ok {
						tradeUpdates = nil
						continue
					}
					if handleErr := processor.ProcessEvent(ctx, update.Event); handleErr != nil {
						logger.Error().Err(handleErr).Str("mint", update.Event.Mint).Str("source", "pumpportal").Msg("failed to process watched pumpportal trade")
					}
					payload, marshalErr := json.Marshal(fiber.Map{
						"type":         "portal_trade_metric",
						"mint":         update.Metric.Mint,
						"buyVolumeSOL": update.Metric.BuyVolumeSOL,
						"buyCount":     update.Metric.BuyCount,
						"detectedAt":   update.Metric.LastTradeAt,
						"updatedAt":    update.Metric.UpdatedAt,
					})
					if marshalErr == nil {
						hub.Broadcast(ws.Message{Priority: ws.PriorityP2High, Payload: payload})
					}
				}

				if tradeUpdates == nil && tradeErrs == nil {
					return
				}
			}
		}()
	}

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
			"authMode":             "telegram",
			"rpcTierAReady":        rpcManager.HasTier(rpc.TierA),
			"rpcTierBReady":        rpcManager.HasTier(rpc.TierB),
			"pumpPortalEnabled":    portalClient.Enabled(),
			"pumpPortalBuyEnabled": portalTradeTracker.Enabled(),
			"telegramEnabled":      telegramNotifier.Enabled(),
			"telegramWebApp":       cfg.Telegram.WebAppURL != "",
			"jitoEnabled":          jitoService.Enabled(),
			"dryRun":               jitoService.DryRun(),
			"ws":                   hub.Stats(),
		})
	})

	app.Get("/api/v1/spikes/recent", func(c *fiber.Ctx) error {
		recent, err := sqliteStore.GetRecentSpikeEvents(c.UserContext(), 100)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load spikes"})
		}
		return c.JSON(fiber.Map{"items": recent})
	})

	app.Get("/api/v1/pump-portal/recent", func(c *fiber.Ctx) error {
		stream := strings.TrimSpace(c.Query("stream"))
		if stream != pumpportal.StreamCreated && stream != pumpportal.StreamMigrated {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "stream must be created or migrated"})
		}
		return c.JSON(fiber.Map{"items": portalBuffer.List(stream, 40)})
	})

	app.Get("/api/v1/pump-portal/watch-stats", func(c *fiber.Ctx) error {
		identity, err := telegramIdentityFromRequest(c, telegramAuth)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "telegram authentication required"})
		}
		return c.JSON(fiber.Map{"userId": identity.UserID, "stats": portalTradeTracker.Stats()})
	})

	app.Post("/api/v1/notifications/test", func(c *fiber.Ctx) error {
		identity, authErr := telegramIdentityFromRequest(c, telegramAuth)
		if authErr != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "telegram authentication required"})
		}

		var req struct {
			ChatID  string `json:"chatId"`
			Message string `json:"message"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}

		msg := strings.TrimSpace(req.Message)
		if msg == "" {
			msg = "Sol Whisperer test notification"
		}

		if err := telegramNotifier.Send(c.UserContext(), strings.TrimSpace(req.ChatID), msg, notification.PriorityHigh); err != nil {
			logger.Warn().Err(err).Str("userId", identity.UserID).Msg("telegram test notification failed")
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(fiber.Map{"status": "sent", "userId": identity.UserID})
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
		portalTradeTracker.AddWatch(req.Mint)
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
		if !listenerRegistry.HasWatchers(req.Mint) {
			portalTradeTracker.RemoveWatch(req.Mint)
		}
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

func telegramIdentityFromRequest(c *fiber.Ctx, verifier *auth.TelegramAuth) (auth.TelegramIdentity, error) {
	if verifier == nil {
		return auth.TelegramIdentity{}, errors.New("telegram verifier not configured")
	}
	initData := strings.TrimSpace(c.Get("X-Telegram-Init-Data"))
	if initData == "" {
		initData = strings.TrimSpace(c.Query("tgInitData"))
	}
	if initData != "" {
		return verifier.VerifyWebAppInitData(initData)
	}

	return auth.TelegramIdentity{}, errors.New("telegram init data missing")
}

func formatCompactRatioForAlert(value float64) string {
	if value <= 0 {
		return "--"
	}
	if value >= 1000 {
		return fmt.Sprintf("%.0fx", value)
	}
	if value >= 100 {
		return fmt.Sprintf("%.1fx", value)
	}
	return fmt.Sprintf("%.2fx", value)
}

func formatSpikeAlert(tokenLabel string, spike volume.SpikeResult, routeTier rpc.Tier, watched bool) string {
	heading := "*Volume Spike*"
	if watched {
		heading = "*Watched Mint Triggered*"
	}

	routeLabel := fmt.Sprintf("%s flow", routeTier)
	if watched && routeTier == rpc.TierA {
		routeLabel = "Tier A watched flow"
	}

	lines := []string{
		heading,
		fmt.Sprintf("*Token:* %s", escapeTelegramMarkdown(tokenLabel)),
		fmt.Sprintf("*Mint:* `%s`", escapeTelegramCode(spike.Mint)),
		fmt.Sprintf("*Route:* %s", escapeTelegramMarkdown(routeLabel)),
		fmt.Sprintf("*Burst:* %s vs baseline", formatCompactRatioForAlert(spike.Ratio)),
		fmt.Sprintf("*5m Buy Volume:* %s SOL", formatCompactSOL(spike.WindowVolumeSOL)),
		fmt.Sprintf("*5m Baseline:* %s SOL", formatCompactSOL(spike.BaselinePer5mSOL)),
		fmt.Sprintf("*Excess Flow:* %s SOL", formatCompactSOL(maxFloat(spike.WindowVolumeSOL-spike.BaselinePer5mSOL, 0))),
		fmt.Sprintf("*Wallets:* %d unique buyers", spike.UniqueWallets),
	}

	if spike.Name != "" {
		lines = append(lines, fmt.Sprintf("*Name:* %s", escapeTelegramMarkdown(spike.Name)))
	}
	if spike.Symbol != "" {
		lines = append(lines, fmt.Sprintf("*Symbol:* %s", escapeTelegramMarkdown(spike.Symbol)))
	}

	if spike.MarketCapSOL > 0 {
		lines = append(lines, fmt.Sprintf("*Market Cap:* %s SOL", formatCompactSOL(spike.MarketCapSOL)))
	}
	if spike.TokenAgeSeconds > 0 {
		lines = append(lines, fmt.Sprintf("*Token Age:* %s", escapeTelegramMarkdown(formatTokenAge(spike.TokenAgeSeconds))))
	}
	if spike.EntryGrade != "" {
		lines = append(lines, fmt.Sprintf("*Entry Grade:* %s", escapeTelegramMarkdown(spike.EntryGrade)))
	}
	if spike.FloorConfidence > 0 {
		lines = append(lines, fmt.Sprintf("*Floor Confidence:* %s", escapeTelegramMarkdown(formatPercent(spike.FloorConfidence))))
	}
	if !spike.DetectedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("*Detected:* %s UTC", escapeTelegramMarkdown(spike.DetectedAt.UTC().Format("15:04:05"))))
	}

	return strings.Join(lines, "\n")
}

func formatCompactSOL(value float64) string {
	if value <= 0 {
		return "--"
	}
	if value >= 1000 {
		return fmt.Sprintf("%.0f", value)
	}
	if value >= 100 {
		return fmt.Sprintf("%.1f", value)
	}
	return fmt.Sprintf("%.2f", value)
}

func formatTokenAge(seconds int64) string {
	if seconds <= 0 {
		return "fresh"
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	}
	return fmt.Sprintf("%dd %dh", seconds/86400, (seconds%86400)/3600)
}

func formatPercent(value float64) string {
	if value <= 1 {
		value *= 100
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%%", value)
	}
	return fmt.Sprintf("%.1f%%", value)
}

func escapeTelegramMarkdown(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"`", "\\`",
	)
	return replacer.Replace(value)
}

func escapeTelegramCode(value string) string {
	return strings.NewReplacer("`", "\\`").Replace(value)
}

func maxFloat(a float64, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
