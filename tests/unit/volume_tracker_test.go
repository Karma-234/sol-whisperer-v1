package unit

import (
	"testing"
	"time"

	"sol-whisperer-v1/internal/volume"
)

func TestTracker_DetectsSpikeWithOrganicWallets(t *testing.T) {
	tracker := volume.NewTracker(2.0, 3)
	now := time.Now().UTC()
	mint := "mintA"

	for i := 0; i < 12; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:      mint,
			Wallet:    "baseline-wallet",
			VolumeSOL: 1,
			At:        now.Add(-55*time.Minute + time.Duration(i)*2*time.Minute),
		})
	}

	for i := 0; i < 3; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:      mint,
			Wallet:    "fresh-wallet-" + string(rune('A'+i)),
			VolumeSOL: 5,
			At:        now.Add(-time.Duration(i) * time.Minute),
		})
	}

	result, detected := tracker.Evaluate(mint, now)
	if !detected {
		t.Fatalf("expected spike detection, got false: %+v", result)
	}
	if result.UniqueWallets < 3 {
		t.Fatalf("expected organic wallets >=3, got %d", result.UniqueWallets)
	}
}

func TestTracker_NoSpikeWhenWalletDiversityWeak(t *testing.T) {
	tracker := volume.NewTracker(2.0, 3)
	now := time.Now().UTC()
	mint := "mintB"

	for i := 0; i < 12; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:      mint,
			Wallet:    "baseline-wallet",
			VolumeSOL: 1,
			At:        now.Add(-55*time.Minute + time.Duration(i)*2*time.Minute),
		})
	}
	for i := 0; i < 4; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:      mint,
			Wallet:    "single-wallet",
			VolumeSOL: 8,
			At:        now.Add(-time.Duration(i) * time.Minute),
		})
	}

	_, detected := tracker.Evaluate(mint, now)
	if detected {
		t.Fatalf("expected no spike due to weak unique-wallet signal")
	}
}
