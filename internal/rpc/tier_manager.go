package rpc

import (
	"errors"
	"sync"
)

type Tier string

const (
	TierA Tier = "A"
	TierB Tier = "B"
)

type Config struct {
	TierARPC []string
	TierAWS  []string
	TierBRPC []string
	TierBWS  []string
}

type endpointSet struct {
	rpc []string
	ws  []string
}

// TierManager owns endpoint selection and failover rotation.
// Encapsulating this here is important so listener-driven tier policies stay
// deterministic and easy to audit.
type TierManager struct {
	mu    sync.Mutex
	tiers map[Tier]endpointSet
	rpcRR map[Tier]int
	wsRR  map[Tier]int
}

func NewTierManager(cfg Config) *TierManager {
	return &TierManager{
		tiers: map[Tier]endpointSet{
			TierA: {rpc: cfg.TierARPC, ws: cfg.TierAWS},
			TierB: {rpc: cfg.TierBRPC, ws: cfg.TierBWS},
		},
		rpcRR: map[Tier]int{TierA: 0, TierB: 0},
		wsRR:  map[Tier]int{TierA: 0, TierB: 0},
	}
}

func (m *TierManager) HasTier(t Tier) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.tiers[t]
	return ok && len(set.rpc) > 0 && len(set.ws) > 0
}

func (m *TierManager) ChooseTierForToken(hasUserListener bool) Tier {
	// User-focused listeners and sniping paths require the fastest/lowest-latency
	// routing, so they are always pinned to Tier A.
	if hasUserListener {
		return TierA
	}
	return TierB
}

func (m *TierManager) NextRPC(t Tier) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.tiers[t]
	if !ok || len(set.rpc) == 0 {
		return "", errors.New("rpc tier has no endpoints")
	}
	idx := m.rpcRR[t] % len(set.rpc)
	m.rpcRR[t] = (m.rpcRR[t] + 1) % len(set.rpc)
	return set.rpc[idx], nil
}

func (m *TierManager) NextWS(t Tier) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.tiers[t]
	if !ok || len(set.ws) == 0 {
		return "", errors.New("ws tier has no endpoints")
	}
	idx := m.wsRR[t] % len(set.ws)
	m.wsRR[t] = (m.wsRR[t] + 1) % len(set.ws)
	return set.ws[idx], nil
}
