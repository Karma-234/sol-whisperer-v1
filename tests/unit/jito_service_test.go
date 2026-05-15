package unit

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.karma-234/sol-whisperer-v1/internal/snipe"
)

func TestJitoService_DryRunSubmit(t *testing.T) {
	logger := zerolog.Nop()
	svc := snipe.NewJitoService(snipe.Config{
		Enabled:       true,
		DryRun:        true,
		DefaultTipSOL: 0.001,
		HTTPClient:    &http.Client{Timeout: time.Second},
		Logger:        logger,
	})

	res, err := svc.SubmitBundle(context.Background(), snipe.BundleRequest{
		ListenerID: "listener-1",
		Mint:       "mint-1",
		SignedTxs:  []string{"base64tx"},
	})
	if err != nil {
		t.Fatalf("submit bundle failed: %v", err)
	}
	if !res.Submitted {
		t.Fatalf("expected submitted in dry-run mode")
	}
	if res.BundleID == "" {
		t.Fatalf("expected dry-run bundle id")
	}
}

func TestJitoService_Disabled(t *testing.T) {
	logger := zerolog.Nop()
	svc := snipe.NewJitoService(snipe.Config{
		Enabled: false,
		DryRun:  true,
		Logger:  logger,
	})

	res, err := svc.SubmitBundle(context.Background(), snipe.BundleRequest{
		SignedTxs: []string{"base64tx"},
	})
	if err != nil {
		t.Fatalf("expected nil err when jito disabled, got %v", err)
	}
	if res.Submitted {
		t.Fatalf("expected non-submitted response when disabled")
	}
}

func TestJitoService_DryRunWithoutAuthKeyUsesDontFrontMarkerMode(t *testing.T) {
	logger := zerolog.Nop()
	svc := snipe.NewJitoService(snipe.Config{
		Enabled:       true,
		DryRun:        true,
		DefaultTipSOL: 0.001,
		HTTPClient:    &http.Client{Timeout: time.Second},
		Logger:        logger,
	})

	res, err := svc.SubmitBundle(context.Background(), snipe.BundleRequest{
		Mint:      "mint-1",
		DontFront: true,
		SignedTxs: []string{"base64tx"},
	})
	if err != nil {
		t.Fatalf("submit bundle failed: %v", err)
	}
	if res.ProtectionMode != "dont-front-marker" {
		t.Fatalf("expected dont-front-marker protection mode, got %q", res.ProtectionMode)
	}
	if res.DontFrontKey != snipe.DefaultDontFrontAccount {
		t.Fatalf("expected default dont-front key, got %q", res.DontFrontKey)
	}
	if !strings.Contains(res.WarningMessage, "dont-front marker") {
		t.Fatalf("expected warning to mention dont-front marker, got %q", res.WarningMessage)
	}
}

func TestJitoService_DryRunWithoutAuthKeyAndWithoutDontFront(t *testing.T) {
	logger := zerolog.Nop()
	svc := snipe.NewJitoService(snipe.Config{
		Enabled:       true,
		DryRun:        true,
		DefaultTipSOL: 0.001,
		HTTPClient:    &http.Client{Timeout: time.Second},
		Logger:        logger,
	})

	res, err := svc.SubmitBundle(context.Background(), snipe.BundleRequest{
		Mint:      "mint-1",
		DontFront: false,
		SignedTxs: []string{"base64tx"},
	})
	if err != nil {
		t.Fatalf("submit bundle failed: %v", err)
	}
	if res.ProtectionMode != "no-auth-key" {
		t.Fatalf("expected no-auth-key protection mode, got %q", res.ProtectionMode)
	}
	if res.DontFrontKey != "" {
		t.Fatalf("expected empty dont-front key, got %q", res.DontFrontKey)
	}
}
