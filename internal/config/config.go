package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the top-level strongly-typed runtime configuration.
// Strong typing prevents silent misconfiguration in financial workflows.
type Config struct {
	App      AppConfig
	Database DatabaseConfig
	RPC      RPCConfig
	PumpDev  PumpDevConfig
	Telegram TelegramConfig
	Jito     JitoConfig
	Sniping  SnipingConfig
}

type AppConfig struct {
	Env  string
	Host string
	Port string
}

type DatabaseConfig struct {
	SQLitePath string
}

type RPCConfig struct {
	TierARPC []string
	TierAWS  []string
	TierBRPC []string
	TierBWS  []string
}

type PumpDevConfig struct {
	WSURL string
}

type TelegramConfig struct {
	Enabled          bool
	BotToken         string
	DefaultChatID    string
	WebAppURL        string
	WebAppButtonText string
}

type JitoConfig struct {
	Enabled        bool
	BlockEngineURL string
	AuthKey        string
	DefaultTipSOL  float64
}

type SnipingConfig struct {
	DryRun bool
}

func Load() (Config, error) {
	// Loading .env is best-effort so production environments can rely entirely on
	// injected environment variables while local dev remains simple.
	_ = godotenv.Load()

	cfg := Config{
		App: AppConfig{
			Env:  getEnv("APP_ENV", "development"),
			Host: getEnv("APP_HOST", "0.0.0.0"),
			Port: getEnv("APP_PORT", "8080"),
		},
		Database: DatabaseConfig{
			SQLitePath: getEnv("SQLITE_PATH", "./data/solwhisperer.db"),
		},
		RPC: RPCConfig{
			TierARPC: splitCSV(getEnv("SOLANA_RPC_TIER_A", "")),
			TierAWS:  splitCSV(getEnv("SOLANA_WS_TIER_A", "")),
			TierBRPC: splitCSV(getEnv("SOLANA_RPC_TIER_B", "")),
			TierBWS:  splitCSV(getEnv("SOLANA_WS_TIER_B", "")),
		},
		PumpDev: PumpDevConfig{
			WSURL: getEnv("PUMPDEV_WS_URL", "wss://pumpdev.io/ws"),
		},
		Telegram: TelegramConfig{
			Enabled:          parseBool(getEnv("TELEGRAM_ENABLED", "false")),
			BotToken:         getEnvSecret("TELEGRAM_BOT_TOKEN", ""),
			DefaultChatID:    getEnvSecret("TELEGRAM_DEFAULT_CHAT_ID", ""),
			WebAppURL:        getEnv("TELEGRAM_WEB_APP_URL", ""),
			WebAppButtonText: getEnv("TELEGRAM_WEB_APP_BUTTON_TEXT", "Open Sol Whisperer"),
		},
		Jito: JitoConfig{
			Enabled:        parseBool(getEnv("JITO_ENABLED", "true")),
			BlockEngineURL: getEnv("JITO_BLOCK_ENGINE_URL", ""),
			AuthKey:        getEnvSecret("JITO_AUTH_KEY", ""),
			DefaultTipSOL:  parseFloat(getEnv("JITO_DEFAULT_TIP_SOL", "0.001")),
		},
		Sniping: SnipingConfig{
			DryRun: parseBool(getEnv("SNIPE_DRY_RUN", "true")),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if len(c.RPC.TierARPC) == 0 || len(c.RPC.TierAWS) == 0 {
		return errors.New("SOLANA_RPC_TIER_A and SOLANA_WS_TIER_A must have at least one endpoint")
	}
	if len(c.RPC.TierBRPC) == 0 || len(c.RPC.TierBWS) == 0 {
		return errors.New("SOLANA_RPC_TIER_B and SOLANA_WS_TIER_B must have at least one endpoint")
	}
	if c.Telegram.BotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is required because authentication is Telegram-based")
	}
	if strings.EqualFold(c.App.Env, "production") && c.Telegram.WebAppURL == "" {
		return errors.New("TELEGRAM_WEB_APP_URL is required in production so users can launch the Telegram Mini App")
	}
	if c.Telegram.WebAppURL != "" && !strings.HasPrefix(c.Telegram.WebAppURL, "https://") {
		return errors.New("TELEGRAM_WEB_APP_URL must be an HTTPS URL")
	}
	if c.Jito.Enabled && !c.Sniping.DryRun && c.Jito.BlockEngineURL == "" {
		return errors.New("JITO_BLOCK_ENGINE_URL is required when JITO_ENABLED=true and SNIPE_DRY_RUN=false")
	}
	if c.Jito.DefaultTipSOL < 0 {
		return errors.New("JITO_DEFAULT_TIP_SOL cannot be negative")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(val)
	}
	return fallback
}

func getEnvSecret(key, fallback string) string {
	if pathRaw, ok := os.LookupEnv(key + "_FILE"); ok {
		path := strings.TrimSpace(pathRaw)
		if path != "" {
			b, err := os.ReadFile(filepath.Clean(path))
			if err == nil {
				v := strings.TrimSpace(string(b))
				if v != "" {
					return v
				}
			}
		}
	}
	return getEnv(key, fallback)
}

func splitCSV(in string) []string {
	parts := strings.Split(in, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseBool(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false
	}
	return b
}

func parseFloat(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return f
}
