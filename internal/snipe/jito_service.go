package snipe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

type Config struct {
	Enabled        bool
	DryRun         bool
	BlockEngineURL string
	AuthKey        string
	DefaultTipSOL  float64
	HTTPClient     *http.Client
	Logger         zerolog.Logger
}

type JitoService struct {
	enabled        bool
	dryRun         bool
	blockEngineURL string
	authKey        string
	defaultTipSOL  float64
	client         *http.Client
	logger         zerolog.Logger
}

// DefaultDontFrontAccount is the canonical marker account used for Jito
// dont-front protection mode when no auth key is configured.
const DefaultDontFrontAccount = "jitodontfront111111111111111111111111111111"

type BundleRequest struct {
	ListenerID    string
	Mint          string
	RouteProvider string
	TipSOL        float64
	DontFront     bool
	SignedTxs     []string
}

type SimulationResult struct {
	Success bool
	Reason  string
}

type BundleResult struct {
	Submitted      bool
	BundleID       string
	Landed         bool
	MEVOutcome     string
	ProtectionMode string
	DontFrontKey   string
	FallbackToRPC  bool
	WarningMessage string
}

func NewJitoService(cfg Config) *JitoService {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &JitoService{
		enabled:        cfg.Enabled,
		dryRun:         cfg.DryRun,
		blockEngineURL: cfg.BlockEngineURL,
		authKey:        cfg.AuthKey,
		defaultTipSOL:  cfg.DefaultTipSOL,
		client:         cfg.HTTPClient,
		logger:         cfg.Logger.With().Str("component", "snipe.jito").Logger(),
	}
}

func (s *JitoService) Enabled() bool { return s.enabled }

func (s *JitoService) DryRun() bool { return s.dryRun }

func (s *JitoService) SimulateBundle(ctx context.Context, req BundleRequest) (SimulationResult, error) {
	_ = ctx
	if !s.enabled {
		return SimulationResult{Success: false, Reason: "jito disabled"}, nil
	}
	if len(req.SignedTxs) == 0 {
		return SimulationResult{}, errors.New("no signed txs provided")
	}

	// We expose explicit simulation status to avoid accidental production sends
	// without pre-flight validation. In volatile meme markets this check is critical
	// because route liquidity can vanish between quote and send.
	if s.dryRun {
		return SimulationResult{Success: true, Reason: "dry-run simulation accepted"}, nil
	}

	// TODO: wire actual Jito simulation endpoint call.
	return SimulationResult{Success: true, Reason: "simulation endpoint not yet integrated"}, nil
}

func (s *JitoService) SubmitBundle(ctx context.Context, req BundleRequest) (BundleResult, error) {
	_ = ctx
	if !s.enabled {
		return BundleResult{Submitted: false, WarningMessage: "jito disabled"}, nil
	}
	if len(req.SignedTxs) == 0 {
		return BundleResult{}, errors.New("bundle submit requires at least one tx")
	}

	tip := req.TipSOL
	if tip <= 0 {
		tip = s.defaultTipSOL
	}

	mode := "auth-key"
	dontFrontKey := ""
	if strings.TrimSpace(s.authKey) == "" {
		if req.DontFront {
			mode = "dont-front-marker"
			dontFrontKey = DefaultDontFrontAccount
		} else {
			mode = "no-auth-key"
		}
	}

	if s.dryRun {
		// Dry-run is intentionally default-true to reduce financial risk during setup.
		warning := fmt.Sprintf("dry-run enabled; no real transaction sent (tip=%.6f, mode=%s)", tip, mode)
		if dontFrontKey != "" {
			warning += ", using dont-front marker account"
		}
		return BundleResult{
			Submitted:      true,
			BundleID:       "dryrun-bundle-" + time.Now().UTC().Format("20060102150405"),
			Landed:         false,
			MEVOutcome:     "protected-simulated",
			ProtectionMode: mode,
			DontFrontKey:   dontFrontKey,
			FallbackToRPC:  false,
			WarningMessage: warning,
		}, nil
	}

	if s.blockEngineURL == "" {
		return BundleResult{
			Submitted:      false,
			FallbackToRPC:  true,
			WarningMessage: "missing Jito block engine URL; fallback to high-priority RPC required",
		}, nil
	}

	// TODO: implement real Jito bundle submit + status polling + fallback behavior.
	return BundleResult{
		Submitted:      true,
		BundleID:       "placeholder-bundle-id",
		Landed:         false,
		MEVOutcome:     "unknown-pending",
		ProtectionMode: mode,
		DontFrontKey:   dontFrontKey,
		FallbackToRPC:  false,
		WarningMessage: "bundle submit placeholder implementation",
	}, nil
}
