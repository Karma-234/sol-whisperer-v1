package volume

import (
	"math"
	"sort"
	"sync"
	"time"
)

type TradeEvent struct {
	Mint         string
	Name         string
	Symbol       string
	Wallet       string
	VolumeSOL    float64
	MarketCapSOL float64
	TxType       string
	At           time.Time
}

type SpikeResult struct {
	Mint                string
	Name                string
	Symbol              string
	WindowVolumeSOL     float64
	BaselinePer5mSOL    float64
	MarketCapSOL        float64
	Ratio               float64
	UniqueWallets       int
	OrganicSignalStrong bool
	TokenCreatedAt      time.Time
	TokenAgeSeconds     int64
	DetectedAt          time.Time
	FloorConfidence     float64
	EntryScore          float64
	EntryGrade          string
}

type mintState struct {
	events         []TradeEvent
	name           string
	symbol         string
	marketCapSOL   float64
	tokenCreatedAt time.Time
	// Rolling history for floor/entry scoring (fixed-size ring buffers for low GC)
	mcapSamples [32]float64
	mcapIdx     int
	bounceCount int
	lastTouchAt time.Time
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
	if e.Name != "" {
		state.name = e.Name
	}
	if e.Symbol != "" {
		state.symbol = e.Symbol
	}
	if e.MarketCapSOL > 0 {
		state.marketCapSOL = e.MarketCapSOL
	}
	if state.tokenCreatedAt.IsZero() || e.TxType == "create" {
		state.tokenCreatedAt = e.At
	}
	state.events = append(state.events, e)
	t.prune(state, e.At)
	// Update rolling mcap for floor scoring
	if e.MarketCapSOL > 0 {
		state.mcapSamples[state.mcapIdx] = e.MarketCapSOL
		state.mcapIdx = (state.mcapIdx + 1) % len(state.mcapSamples)
		state.lastTouchAt = e.At
	}
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
		if ev.VolumeSOL <= 0 {
			continue
		}
		if ev.At.After(windowStart) || ev.At.Equal(windowStart) {
			windowVol += ev.VolumeSOL
			wallets[ev.Wallet] = struct{}{}
		}
		if (ev.At.After(baselineStart) || ev.At.Equal(baselineStart)) && ev.At.Before(windowStart) {
			baselineVol += ev.VolumeSOL
		}
	}

	// Compare current 5m window against the preceding 55m buy flow (11 x 5m buckets).
	baselinePer5m := baselineVol / 11.0
	if baselinePer5m <= 0 {
		baselinePer5m = 0.0000001
	}
	ratio := windowVol / baselinePer5m
	uniqueWallets := len(wallets)
	organic := uniqueWallets >= t.walletMin
	detected := ratio > t.spikeRatioMin && organic

	// Compute floor confidence and entry score
	floorConf := t.computeFloorConfidence(state, windowVol, now)
	entryScore, entryGrade := t.computeEntryScore(state, ratio, uniqueWallets, windowVol, baselinePer5m, floorConf, organic)

	result := SpikeResult{
		Mint:                mint,
		Name:                state.name,
		Symbol:              state.symbol,
		WindowVolumeSOL:     round(windowVol),
		BaselinePer5mSOL:    round(baselinePer5m),
		MarketCapSOL:        round(state.marketCapSOL),
		Ratio:               round(ratio),
		UniqueWallets:       uniqueWallets,
		OrganicSignalStrong: organic,
		TokenCreatedAt:      state.tokenCreatedAt,
		DetectedAt:          now,
		FloorConfidence:     round(floorConf),
		EntryScore:          round(entryScore),
		EntryGrade:          entryGrade,
	}
	if !state.tokenCreatedAt.IsZero() && now.After(state.tokenCreatedAt) {
		result.TokenAgeSeconds = int64(now.Sub(state.tokenCreatedAt).Seconds())
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

// computeFloorConfidence uses rolling mcap samples to estimate price floor robustness.
// floor_confidence = 0.35*S_support + 0.30*S_flow + 0.20*S_depth + 0.15*S_dist
func (t *Tracker) computeFloorConfidence(state *mintState, windowVol float64, now time.Time) float64 {
	if windowVol <= 0 {
		return 0
	}

	// S_support: distance above rolling support level (q20 of non-zero mcap samples)
	s_support := t.scoreSupport(state)

	// S_flow: bounce behavior near support (% of samples within ±3% of support)
	s_flow := t.scoreFlow(state)

	// S_depth: liquidity proxy from unique buyer growth
	s_depth := t.scoreDepth(state)

	// S_dist: wallet concentration penalty
	s_dist := t.scoreConcentration(state)

	floorConf := 0.35*s_support + 0.30*s_flow + 0.20*s_depth + 0.15*s_dist
	if floorConf > 1.0 {
		floorConf = 1.0
	}
	return floorConf
}

// scoreSupport: Normalize distance above rolling q20 as fraction [0, 1]
func (t *Tracker) scoreSupport(state *mintState) float64 {
	support := quantileNonZero(state.mcapSamples[:], 0.20)
	if support == 0 || state.marketCapSOL == 0 {
		return 0.5 // neutral
	}
	distance := (state.marketCapSOL - support) / state.marketCapSOL
	if distance < 0 {
		return 0
	}
	if distance > 1 {
		return 1
	}
	return distance
}

// scoreFlow: Estimate bounce resilience from recent mcap touches near support [0, 1]
func (t *Tracker) scoreFlow(state *mintState) float64 {
	support := quantileNonZero(state.mcapSamples[:], 0.20)
	if support == 0 {
		return 0.3
	}
	tolerance := support * 0.03 // ±3% band
	count := 0
	for _, mcap := range state.mcapSamples {
		if mcap > 0 && mcap >= support && mcap <= support+tolerance {
			count++
		}
	}
	return float64(count) / float64(len(state.mcapSamples))
}

// scoreDepth: Unique buyer growth as proxy for depth [0, 1]
func (t *Tracker) scoreDepth(state *mintState) float64 {
	buyers := uniqueBuyers(state.events)
	if buyers < 5 {
		return 0.3
	}
	// Normalize: at 20+ unique buyers, score = 1.0
	depth := float64(buyers) / 20.0
	if depth > 1 {
		return 1
	}
	return depth
}

// scoreConcentration: Wallet concentration penalty [0, 1], higher = more distributed
func (t *Tracker) scoreConcentration(state *mintState) float64 {
	walletsMap := make(map[string]float64)
	for _, ev := range state.events {
		if ev.VolumeSOL > 0 && ev.Wallet != "" {
			walletsMap[ev.Wallet] += ev.VolumeSOL
		}
	}
	if len(walletsMap) < 3 {
		return 0.2
	}
	totalVol := 0.0
	for _, vol := range walletsMap {
		totalVol += vol
	}
	if totalVol <= 0 {
		return 0.2
	}
	// Penalty if top wallet > 40% of volume
	maxWalletVol := 0.0
	for _, vol := range walletsMap {
		if vol > maxWalletVol {
			maxWalletVol = vol
		}
	}
	topFraction := float64(maxWalletVol) / float64(totalVol)
	if topFraction > 0.4 {
		return 0.2 // concentrated, low score
	}
	return 0.8 // distributed
}

// computeEntryScore: Composite entry signal grade [0, 100]
// entry_score = 30*R + 20*U + 15*P + 15*F + 10*L + 10*S
func (t *Tracker) computeEntryScore(state *mintState, ratio float64, uniqueWallets int, windowVol, baselinePer5m, floorConf float64, organic bool) (float64, string) {
	if !organic {
		return 0, "Reject"
	}

	// R: Normalized ratio impulse [0, 30]
	r := t.scoreRatioImpulse(ratio)

	// U: Unique buyer growth [0, 20]
	u := t.scoreUniqueGrowth(uniqueWallets)

	// P: Persistence multi-window confirmation [0, 15]
	p := t.scorePersistence(state, windowVol, baselinePer5m)

	// F: Floor confidence mapped [0, 15]
	f := floorConf

	// L: Liquidity/depth quality [0, 10]
	l := t.scoreDepth(state)

	// S: Market structure quality [0, 10]
	s := t.scoreStructure(state)

	entry := 30*r + 20*u + 15*p + 15*f + 10*l + 10*s
	if entry > 100 {
		entry = 100
	}

	grade := t.gradeEntry(entry, floorConf)
	return entry, grade
}

// scoreRatioImpulse: Saturate ratio impulse into [0, 1], map to [0, 30]
func (t *Tracker) scoreRatioImpulse(ratio float64) float64 {
	// At ratio=12, score ≈ 0.5; at ratio>30, score = 1.0
	if ratio < t.spikeRatioMin {
		return 0
	}
	denom := 30 - t.spikeRatioMin
	if denom <= 0 {
		return 1
	}
	norm := (ratio - t.spikeRatioMin) / denom
	if norm < 0 {
		return 0
	}
	if norm > 1 {
		norm = 1
	}
	return norm
}

// scoreUniqueGrowth: Map unique wallet count [5, 50] to [0, 1], scale to [0, 20]
func (t *Tracker) scoreUniqueGrowth(uniqueWallets int) float64 {
	if uniqueWallets < 5 {
		return 0
	}
	// At 5 wallets, score ≈ 0.3; at 50+ wallets, score = 1.0
	norm := float64(uniqueWallets-5) / 45.0
	if norm > 1 {
		norm = 1
	}
	return norm
}

// scorePersistence: Check sustained activity across multiple windows [0, 1]
func (t *Tracker) scorePersistence(state *mintState, windowVol, baselinePer5m float64) float64 {
	if windowVol < baselinePer5m*1.5 {
		return 0.3 // weak multi-window confirmation
	}
	return 0.8 // sustained activity signal
}

// scoreStructure: Detect higher lows and reclaim quality [0, 1]
func (t *Tracker) scoreStructure(state *mintState) float64 {
	if len(state.events) < 10 {
		return 0.5
	}
	// Simple heuristic: if recent mcap is near-max, structure is strong
	maxMcap := 0.0
	for _, ev := range state.events {
		if ev.MarketCapSOL > maxMcap {
			maxMcap = ev.MarketCapSOL
		}
	}
	if maxMcap == 0 || state.marketCapSOL == 0 {
		return 0.5
	}
	ratio := state.marketCapSOL / maxMcap
	if ratio > 0.8 {
		return 0.9 // strong structure
	}
	if ratio > 0.6 {
		return 0.6
	}
	return 0.3
}

// gradeEntry: Assign letter grade based on entry_score and floor_confidence
func (t *Tracker) gradeEntry(entryScore, floorConf float64) string {
	if entryScore >= 80 && floorConf >= 0.70 {
		return "A"
	}
	if entryScore >= 70 && floorConf >= 0.65 {
		return "B"
	}
	if entryScore >= 60 {
		return "C"
	}
	return "Reject"
}

func uniqueBuyers(events []TradeEvent) int {
	wallets := make(map[string]struct{})
	for _, ev := range events {
		if ev.VolumeSOL <= 0 || ev.Wallet == "" {
			continue
		}
		wallets[ev.Wallet] = struct{}{}
	}
	return len(wallets)
}

func quantileNonZero(values []float64, q float64) float64 {
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	filtered := make([]float64, 0, len(values))
	for _, v := range values {
		if v > 0 {
			filtered = append(filtered, v)
		}
	}
	if len(filtered) == 0 {
		return 0
	}
	sort.Float64s(filtered)
	idx := int(math.Round(q * float64(len(filtered)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(filtered) {
		idx = len(filtered) - 1
	}
	return filtered[idx]
}
