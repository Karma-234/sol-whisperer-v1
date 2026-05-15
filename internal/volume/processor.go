package volume

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.karma-234/sol-whisperer-v1/internal/pumpdev"
	"github.karma-234/sol-whisperer-v1/internal/store"
)

type SnapshotStore interface {
	InsertVolumeSnapshot(ctx context.Context, mint string, windowStart time.Time, windowEnd time.Time, volumeSOL float64, uniqueWallets int) error
	InsertSpikeEvent(ctx context.Context, rec store.SpikeEventRecord) error
}

type ProcessorConfig struct {
	MinSpikeEmitInterval time.Duration
	TxDedupeWindow       time.Duration
}

type Processor struct {
	tracker      *Tracker
	store        SnapshotStore
	onSpike      func(SpikeResult)
	logger       zerolog.Logger
	minEmitDelta time.Duration
	lastSpikeAt  map[string]time.Time
	txSigMu      sync.Mutex
	txSigCache   map[string]time.Time
	dedupeWindow time.Duration
}

func NewProcessor(tracker *Tracker, store SnapshotStore, onSpike func(SpikeResult), logger zerolog.Logger, cfg ProcessorConfig) *Processor {
	if cfg.MinSpikeEmitInterval <= 0 {
		cfg.MinSpikeEmitInterval = 20 * time.Second
	}
	if cfg.TxDedupeWindow <= 0 {
		cfg.TxDedupeWindow = 5 * time.Minute
	}
	if onSpike == nil {
		onSpike = func(SpikeResult) {}
	}
	return &Processor{
		tracker:      tracker,
		store:        store,
		onSpike:      onSpike,
		logger:       logger.With().Str("component", "volume.processor").Logger(),
		minEmitDelta: cfg.MinSpikeEmitInterval,
		lastSpikeAt:  make(map[string]time.Time),
		txSigCache:   make(map[string]time.Time),
		dedupeWindow: cfg.TxDedupeWindow,
	}
}

func (p *Processor) Run(ctx context.Context, events <-chan pumpdev.Event, errs <-chan error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				p.logger.Error().Err(err).Msg("pumpdev stream error")
			}
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if handleErr := p.ProcessEvent(ctx, ev); handleErr != nil {
				p.logger.Error().Err(handleErr).Str("mint", ev.Mint).Msg("failed to process trade event")
			}
		}
	}
}

// RunDual consumes from both PumpDev and Tier A RPC sources, fanning both into ProcessEvent.
// It returns when context is canceled.
func (p *Processor) RunDual(ctx context.Context, pumpEvents <-chan pumpdev.Event, pumpErrs <-chan error, rpcEvents <-chan pumpdev.Event, rpcErrs <-chan error) error {
	pumpOpen, rpcOpen := true, true

	for {
		select {
		case <-ctx.Done():
			return nil

		case err, ok := <-pumpErrs:
			if !ok {
				pumpErrs = nil
				pumpOpen = false
				continue
			}
			if err != nil {
				p.logger.Error().Err(err).Msg("pumpdev stream error")
			}

		case err, ok := <-rpcErrs:
			if !ok {
				rpcErrs = nil
				rpcOpen = false
				continue
			}
			if err != nil {
				p.logger.Error().Err(err).Msg("rpc stream error")
			}

		case ev, ok := <-pumpEvents:
			if !ok {
				pumpOpen = false
				if !rpcOpen {
					return nil
				}
				continue
			}
			if handleErr := p.ProcessEvent(ctx, ev); handleErr != nil {
				p.logger.Error().Err(handleErr).Str("mint", ev.Mint).Str("source", "pumpdev").Msg("failed to process trade event")
			}

		case ev, ok := <-rpcEvents:
			if !ok {
				rpcOpen = false
				if !pumpOpen {
					return nil
				}
				continue
			}
			if handleErr := p.ProcessEvent(ctx, ev); handleErr != nil {
				p.logger.Error().Err(handleErr).Str("mint", ev.Mint).Str("source", "rpc").Msg("failed to process trade event")
			}
		}

		// Stop if both sources are exhausted
		if !pumpOpen && !rpcOpen {
			return nil
		}
	}
}

func (p *Processor) ProcessEvent(ctx context.Context, ev pumpdev.Event) error {
	// Dedup check: skip if signature was seen recently
	if ev.Signature != "" && !p.checkAndMarkSigSeen(ev.Signature) {
		return nil
	}

	at := ev.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	txType := normalizeTxType(ev.TxType)
	trade := TradeEvent{
		Mint:         ev.Mint,
		Name:         ev.Name,
		Symbol:       ev.Symbol,
		Wallet:       ev.TraderAddress,
		VolumeSOL:    ev.VolumeSOL,
		MarketCapSOL: ev.MarketCapSOL,
		TxType:       txType,
		At:           at,
	}

	if txType == "create" {
		trade.VolumeSOL = 0
		trade.Wallet = ""
		p.tracker.AddEvent(trade)
		return nil
	}

	if txType != "buy" {
		return nil
	}

	p.tracker.AddEvent(trade)
	result, detected := p.tracker.Evaluate(ev.Mint, at)

	if err := p.store.InsertVolumeSnapshot(ctx, ev.Mint, at.Add(-5*time.Minute), at, result.WindowVolumeSOL, result.UniqueWallets); err != nil {
		return fmt.Errorf("persist volume snapshot: %w", err)
	}

	if !detected {
		return nil
	}
	if !p.shouldEmitSpike(ev.Mint, at) {
		return nil
	}

	record := store.SpikeEventRecord{
		ID:              buildSpikeID(ev.Mint, at),
		Mint:            ev.Mint,
		Name:            result.Name,
		Symbol:          result.Symbol,
		Ratio:           result.Ratio,
		WindowVolume:    result.WindowVolumeSOL,
		BaselinePer5m:   result.BaselinePer5mSOL,
		MarketCapSOL:    result.MarketCapSOL,
		UniqueWallets:   result.UniqueWallets,
		TokenCreatedAt:  result.TokenCreatedAt,
		TokenAgeSeconds: result.TokenAgeSeconds,
		FloorConfidence: result.FloorConfidence,
		EntryScore:      result.EntryScore,
		EntryGrade:      result.EntryGrade,
		CreatedAt:       at,
	}
	if err := p.store.InsertSpikeEvent(ctx, record); err != nil {
		return fmt.Errorf("persist spike event: %w", err)
	}

	p.onSpike(result)
	p.logger.Info().
		Str("mint", result.Mint).
		Float64("ratio", result.Ratio).
		Float64("window_volume_sol", result.WindowVolumeSOL).
		Float64("baseline_per5m_sol", result.BaselinePer5mSOL).
		Int("unique_wallets", result.UniqueWallets).
		Msg("volume spike detected")
	return nil
}

func (p *Processor) shouldEmitSpike(mint string, at time.Time) bool {
	last, ok := p.lastSpikeAt[mint]
	if ok && at.Sub(last) < p.minEmitDelta {
		return false
	}
	p.lastSpikeAt[mint] = at
	return true
}

// checkAndMarkSigSeen returns true if the signature is new (not seen recently).
// It also marks the signature as seen for dedup purposes.
// Expired entries are cleaned up lazily.
func (p *Processor) checkAndMarkSigSeen(sig string) bool {
	p.txSigMu.Lock()
	defer p.txSigMu.Unlock()

	now := time.Now().UTC()

	// Check if signature was seen recently
	if lastSeen, ok := p.txSigCache[sig]; ok {
		if now.Sub(lastSeen) < p.dedupeWindow {
			return false // Duplicate
		}
	}

	// Mark as seen now
	p.txSigCache[sig] = now

	// Lazy cleanup: remove old entries if cache is getting large
	if len(p.txSigCache) > 10000 {
		cutoff := now.Add(-p.dedupeWindow)
		for s, t := range p.txSigCache {
			if t.Before(cutoff) {
				delete(p.txSigCache, s)
			}
		}
	}

	return true // New signature
}

func buildSpikeID(mint string, at time.Time) string {
	return fmt.Sprintf("%s-%d", mint, at.UTC().UnixNano())
}

func normalizeTxType(txType string) string {
	return strings.ToLower(strings.TrimSpace(txType))
}
