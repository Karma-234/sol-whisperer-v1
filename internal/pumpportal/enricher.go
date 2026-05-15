package pumpportal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type DexScreenerEnricher struct {
	client *http.Client
	logger zerolog.Logger

	mu    sync.Mutex
	cache map[string]cachedEvent
}

type cachedEvent struct {
	ev        Event
	expiresAt time.Time
}

type dexPair struct {
	DexID       string `json:"dexId"`
	PairAddress string `json:"pairAddress"`
	BaseToken   struct {
		Address string `json:"address"`
		Name    string `json:"name"`
		Symbol  string `json:"symbol"`
	} `json:"baseToken"`
	QuoteToken struct {
		Address string `json:"address"`
		Name    string `json:"name"`
		Symbol  string `json:"symbol"`
	} `json:"quoteToken"`
	PriceUsd    string  `json:"priceUsd"`
	PriceNative string  `json:"priceNative"`
	FDV         float64 `json:"fdv"`
	MarketCap   float64 `json:"marketCap"`
	Liquidity   struct {
		USD float64 `json:"usd"`
	} `json:"liquidity"`
	Volume struct {
		M5 float64 `json:"m5"`
		H1 float64 `json:"h1"`
	} `json:"volume"`
	Txns struct {
		M5 struct {
			Buys  int `json:"buys"`
			Sells int `json:"sells"`
		} `json:"m5"`
	} `json:"txns"`
	PairCreatedAt int64 `json:"pairCreatedAt"`
	Info          struct {
		ImageURL string `json:"imageUrl"`
		Websites []struct {
			URL string `json:"url"`
		} `json:"websites"`
		Socials []struct {
			Platform string `json:"platform"`
			Handle   string `json:"handle"`
		} `json:"socials"`
	} `json:"info"`
}

func NewDexScreenerEnricher(client *http.Client, logger zerolog.Logger) *DexScreenerEnricher {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	return &DexScreenerEnricher{
		client: client,
		logger: logger.With().Str("component", "pumpportal.dexscreener_enricher").Logger(),
		cache:  make(map[string]cachedEvent),
	}
}

func (e *DexScreenerEnricher) Enrich(ctx context.Context, ev Event) Event {
	if ev.Stream != StreamMigrated || strings.TrimSpace(ev.Mint) == "" {
		return ev
	}

	if cached, ok := e.cached(ev.Mint); ok {
		return mergeEnrichedEvent(ev, cached)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.dexscreener.com/token-pairs/v1/solana/%s", ev.Mint), nil)
	if err != nil {
		return ev
	}

	res, err := e.client.Do(req)
	if err != nil {
		e.logger.Debug().Err(err).Str("mint", ev.Mint).Msg("dexscreener enrichment request failed")
		return ev
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		e.logger.Debug().Int("status", res.StatusCode).Str("mint", ev.Mint).Msg("dexscreener enrichment returned non-success")
		return ev
	}

	var pairs []dexPair
	if err := json.NewDecoder(res.Body).Decode(&pairs); err != nil {
		e.logger.Debug().Err(err).Str("mint", ev.Mint).Msg("failed to decode dexscreener enrichment payload")
		return ev
	}

	best, ok := selectBestPair(pairs)
	if !ok {
		return ev
	}

	enriched := ev
	if strings.TrimSpace(enriched.Name) == "" {
		enriched.Name = bestTokenName(best, ev.Mint)
	}
	if strings.TrimSpace(enriched.Symbol) == "" {
		enriched.Symbol = bestTokenSymbol(best, ev.Mint)
	}
	enriched.DexID = strings.TrimSpace(best.DexID)
	enriched.PairAddress = strings.TrimSpace(best.PairAddress)
	enriched.PriceUSD = parseNumberString(best.PriceUsd)
	enriched.PriceNative = parseNumberString(best.PriceNative)
	enriched.FDV = best.FDV
	enriched.MarketCapUSD = best.MarketCap
	enriched.LiquidityUSD = best.Liquidity.USD
	enriched.Volume5mUSD = best.Volume.M5
	enriched.Volume1hUSD = best.Volume.H1
	enriched.Buys5m = best.Txns.M5.Buys
	enriched.Sells5m = best.Txns.M5.Sells
	if best.PairCreatedAt > 0 {
		enriched.PairCreatedAt = time.UnixMilli(best.PairCreatedAt).UTC()
	}
	enriched.ImageURL = strings.TrimSpace(best.Info.ImageURL)
	enriched.WebsiteURL = firstURL(best.Info.Websites)
	enriched.SocialHandle = firstSocial(best.Info.Socials)

	e.store(enriched)
	return enriched
}

func (e *DexScreenerEnricher) cached(mint string) (Event, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	entry, ok := e.cache[mint]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(e.cache, mint)
		}
		return Event{}, false
	}
	return entry.ev, true
}

func (e *DexScreenerEnricher) store(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache[ev.Mint] = cachedEvent{ev: ev, expiresAt: time.Now().Add(2 * time.Minute)}
}

func selectBestPair(pairs []dexPair) (dexPair, bool) {
	if len(pairs) == 0 {
		return dexPair{}, false
	}
	best := pairs[0]
	bestScore := scorePair(best)
	for _, pair := range pairs[1:] {
		score := scorePair(pair)
		if score > bestScore {
			best = pair
			bestScore = score
		}
	}
	return best, true
}

func scorePair(pair dexPair) float64 {
	return pair.Liquidity.USD + pair.Volume.H1 + pair.Volume.M5*4
}

func parseNumberString(value string) float64 {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	var out json.Number = json.Number(strings.TrimSpace(value))
	parsed, err := out.Float64()
	if err != nil {
		return 0
	}
	return parsed
}

func firstURL(items []struct {
	URL string "json:\"url\""
}) string {
	for _, item := range items {
		if strings.TrimSpace(item.URL) != "" {
			return strings.TrimSpace(item.URL)
		}
	}
	return ""
}

func firstSocial(items []struct {
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
}) string {
	for _, item := range items {
		platform := strings.TrimSpace(item.Platform)
		handle := strings.TrimSpace(item.Handle)
		if platform != "" && handle != "" {
			return platform + ":" + handle
		}
	}
	return ""
}

func mergeEnrichedEvent(base Event, enriched Event) Event {
	if strings.TrimSpace(base.Name) == "" {
		base.Name = enriched.Name
	}
	if strings.TrimSpace(base.Symbol) == "" {
		base.Symbol = enriched.Symbol
	}
	base.DexID = enriched.DexID
	base.PairAddress = enriched.PairAddress
	base.PriceUSD = enriched.PriceUSD
	base.PriceNative = enriched.PriceNative
	base.FDV = enriched.FDV
	base.MarketCapUSD = enriched.MarketCapUSD
	base.LiquidityUSD = enriched.LiquidityUSD
	base.Volume5mUSD = enriched.Volume5mUSD
	base.Volume1hUSD = enriched.Volume1hUSD
	base.Buys5m = enriched.Buys5m
	base.Sells5m = enriched.Sells5m
	base.PairCreatedAt = enriched.PairCreatedAt
	base.ImageURL = enriched.ImageURL
	base.WebsiteURL = enriched.WebsiteURL
	base.SocialHandle = enriched.SocialHandle
	return base
}

func bestTokenName(pair dexPair, mint string) string {
	if tokenMatchesMint(pair.BaseToken.Address, mint) && strings.TrimSpace(pair.BaseToken.Name) != "" {
		return strings.TrimSpace(pair.BaseToken.Name)
	}
	if tokenMatchesMint(pair.QuoteToken.Address, mint) && strings.TrimSpace(pair.QuoteToken.Name) != "" {
		return strings.TrimSpace(pair.QuoteToken.Name)
	}
	if strings.TrimSpace(pair.BaseToken.Name) != "" {
		return strings.TrimSpace(pair.BaseToken.Name)
	}
	if strings.TrimSpace(pair.QuoteToken.Name) != "" {
		return strings.TrimSpace(pair.QuoteToken.Name)
	}
	return ""
}

func bestTokenSymbol(pair dexPair, mint string) string {
	if tokenMatchesMint(pair.BaseToken.Address, mint) && strings.TrimSpace(pair.BaseToken.Symbol) != "" {
		return strings.TrimSpace(pair.BaseToken.Symbol)
	}
	if tokenMatchesMint(pair.QuoteToken.Address, mint) && strings.TrimSpace(pair.QuoteToken.Symbol) != "" {
		return strings.TrimSpace(pair.QuoteToken.Symbol)
	}
	if strings.TrimSpace(pair.BaseToken.Symbol) != "" {
		return strings.TrimSpace(pair.BaseToken.Symbol)
	}
	if strings.TrimSpace(pair.QuoteToken.Symbol) != "" {
		return strings.TrimSpace(pair.QuoteToken.Symbol)
	}
	return ""
}

func tokenMatchesMint(tokenAddress, mint string) bool {
	return strings.EqualFold(strings.TrimSpace(tokenAddress), strings.TrimSpace(mint))
}
