package volume

import (
	"math"
	"sort"
	"sync"
	"time"
)

type TradeEvent struct {
	Mint      string
	Wallet    string
	VolumeSOL float64
	At        time.Time
}

type SpikeResult struct {
	Mint                string
	WindowVolumeSOL     float64
	BaselinePer5mSOL    float64
	Ratio               float64
	UniqueWallets       int
	OrganicSignalStrong bool
	DetectedAt          time.Time
}

type mintState struct {
	events []TradeEvent
}

// Tracker computes 5-minute rolling volume against 1-hour baseline.
// Keeping the logic deterministic and in-memory first makes unit testing easy;
// persistence integration can then snapshot these metrics without changing math.
type Tracker struct {
	mu            sync.Mutex
	states        map[string]*mintState
	spikeRatioMin float64
	walletMin     int
}

func NewTracker(spikeRatioMin float64, walletMin int) *Tracker {
	if spikeRatioMin <= 0 {
		spikeRatioMin = 2.0
	}
	if walletMin <= 0 {
		walletMin = 5
	}
	return &Tracker{
		states:        make(map[string]*mintState),
		spikeRatioMin: spikeRatioMin,
		walletMin:     walletMin,
	}
}

func (t *Tracker) AddEvent(e TradeEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.ensureMint(e.Mint)
	state.events = append(state.events, e)
	t.prune(state, e.At)
}

func (t *Tracker) Evaluate(mint string, now time.Time) (SpikeResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.states[mint]
	if !ok {
		return SpikeResult{}, false
	}
	t.prune(state, now)

	windowStart := now.Add(-5 * time.Minute)
	baselineStart := now.Add(-60 * time.Minute)

	var windowVol float64
	var baselineVol float64
	wallets := make(map[string]struct{})

	for _, ev := range state.events {
		if ev.At.After(windowStart) || ev.At.Equal(windowStart) {
			windowVol += ev.VolumeSOL
			wallets[ev.Wallet] = struct{}{}
		}
		if ev.At.After(baselineStart) || ev.At.Equal(baselineStart) {
			baselineVol += ev.VolumeSOL
		}
	}

	baselinePer5m := baselineVol / 12.0
	if baselinePer5m <= 0 {
		baselinePer5m = 0.0000001
	}
	ratio := windowVol / baselinePer5m
	uniqueWallets := len(wallets)
	organic := uniqueWallets >= t.walletMin
	detected := ratio >= t.spikeRatioMin && organic

	result := SpikeResult{
		Mint:                mint,
		WindowVolumeSOL:     round(windowVol),
		BaselinePer5mSOL:    round(baselinePer5m),
		Ratio:               round(ratio),
		UniqueWallets:       uniqueWallets,
		OrganicSignalStrong: organic,
		DetectedAt:          now,
	}
	return result, detected
}

func (t *Tracker) ensureMint(mint string) *mintState {
	if state, ok := t.states[mint]; ok {
		return state
	}
	state := &mintState{events: make([]TradeEvent, 0, 128)}
	t.states[mint] = state
	return state
}

func (t *Tracker) prune(state *mintState, now time.Time) {
	cutoff := now.Add(-60 * time.Minute)
	idx := sort.Search(len(state.events), func(i int) bool {
		return state.events[i].At.After(cutoff) || state.events[i].At.Equal(cutoff)
	})
	if idx > 0 && idx < len(state.events) {
		state.events = append([]TradeEvent(nil), state.events[idx:]...)
	} else if idx >= len(state.events) {
		state.events = state.events[:0]
	}
}

func round(v float64) float64 {
	return math.Round(v*1_000_000) / 1_000_000
}
