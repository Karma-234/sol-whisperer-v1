package volume

import (
	"context"
	"fmt"
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
}

type Processor struct {
	tracker      *Tracker
	store        SnapshotStore
	onSpike      func(SpikeResult)
	logger       zerolog.Logger
	minEmitDelta time.Duration
	lastSpikeAt  map[string]time.Time
}

func NewProcessor(tracker *Tracker, store SnapshotStore, onSpike func(SpikeResult), logger zerolog.Logger, cfg ProcessorConfig) *Processor {
	if cfg.MinSpikeEmitInterval <= 0 {
		cfg.MinSpikeEmitInterval = 20 * time.Second
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

func (p *Processor) ProcessEvent(ctx context.Context, ev pumpdev.Event) error {
	at := ev.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	trade := TradeEvent{
		Mint:      ev.Mint,
		Wallet:    ev.TraderAddress,
		VolumeSOL: ev.VolumeSOL,
		At:        at,
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
		ID:            buildSpikeID(ev.Mint, at),
		Mint:          ev.Mint,
		Ratio:         result.Ratio,
		WindowVolume:  result.WindowVolumeSOL,
		BaselinePer5m: result.BaselinePer5mSOL,
		UniqueWallets: result.UniqueWallets,
		CreatedAt:     at,
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

func buildSpikeID(mint string, at time.Time) string {
	return fmt.Sprintf("%s-%d", mint, at.UTC().UnixNano())
}
