package unit

import (
	"testing"
	"time"

	"github.karma-234/sol-whisperer-v1/internal/volume"
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

func TestTracker_EntryScoreScalesWithRatio(t *testing.T) {
	tracker := volume.NewTracker(2.0, 3)
	now := time.Now().UTC()

	seedBaseline := func(mint string) {
		for i := 0; i < 12; i++ {
			tracker.AddEvent(volume.TradeEvent{
				Mint:      mint,
				Wallet:    "baseline-wallet",
				VolumeSOL: 1,
				At:        now.Add(-55*time.Minute + time.Duration(i)*2*time.Minute),
			})
		}
	}

	seedBaseline("mint-low-ratio")
	seedBaseline("mint-high-ratio")

	for i := 0; i < 3; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:         "mint-low-ratio",
			Wallet:       "low-wallet-" + string(rune('A'+i)),
			VolumeSOL:    3,
			MarketCapSOL: 20,
			At:           now.Add(-time.Duration(i) * time.Minute),
		})
		tracker.AddEvent(volume.TradeEvent{
			Mint:         "mint-high-ratio",
			Wallet:       "high-wallet-" + string(rune('A'+i)),
			VolumeSOL:    9,
			MarketCapSOL: 20,
			At:           now.Add(-time.Duration(i) * time.Minute),
		})
	}

	low, detectedLow := tracker.Evaluate("mint-low-ratio", now)
	high, detectedHigh := tracker.Evaluate("mint-high-ratio", now)
	if !detectedLow || !detectedHigh {
		t.Fatalf("expected both mints to detect as spikes: low=%v high=%v", detectedLow, detectedHigh)
	}
	if low.EntryScore >= high.EntryScore {
		t.Fatalf("expected higher-ratio signal to have higher entry score, low=%f high=%f", low.EntryScore, high.EntryScore)
	}
	if low.EntryScore >= 100 || high.EntryScore >= 100 {
		t.Fatalf("expected non-saturated entry scores, low=%f high=%f", low.EntryScore, high.EntryScore)
	}
}

func TestTracker_DepthUsesUniqueBuyersNotTradeCount(t *testing.T) {
	tracker := volume.NewTracker(2.0, 1)
	now := time.Now().UTC()

	for i := 0; i < 12; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:      "mint-single-wallet",
			Wallet:    "baseline-wallet",
			VolumeSOL: 1,
			At:        now.Add(-55*time.Minute + time.Duration(i)*2*time.Minute),
		})
		tracker.AddEvent(volume.TradeEvent{
			Mint:      "mint-many-wallets",
			Wallet:    "baseline-wallet",
			VolumeSOL: 1,
			At:        now.Add(-55*time.Minute + time.Duration(i)*2*time.Minute),
		})
	}

	for i := 0; i < 12; i++ {
		tracker.AddEvent(volume.TradeEvent{
			Mint:         "mint-single-wallet",
			Wallet:       "single-buyer",
			VolumeSOL:    2,
			MarketCapSOL: 30,
			At:           now.Add(-time.Duration(i) * 20 * time.Second),
		})
		tracker.AddEvent(volume.TradeEvent{
			Mint:         "mint-many-wallets",
			Wallet:       "multi-buyer-" + string(rune('A'+i)),
			VolumeSOL:    2,
			MarketCapSOL: 30,
			At:           now.Add(-time.Duration(i) * 20 * time.Second),
		})
	}

	single, _ := tracker.Evaluate("mint-single-wallet", now)
	multi, _ := tracker.Evaluate("mint-many-wallets", now)
	if single.FloorConfidence >= multi.FloorConfidence {
		t.Fatalf("expected many unique buyers to improve confidence, single=%f multi=%f", single.FloorConfidence, multi.FloorConfidence)
	}
}
