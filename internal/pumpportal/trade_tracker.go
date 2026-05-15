package pumpportal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/rs/zerolog"

	"github.karma-234/sol-whisperer-v1/internal/pumpdev"
)

type TradeMetric struct {
	Mint         string    `json:"mint"`
	BuyVolumeSOL float64   `json:"buyVolumeSOL"`
	BuyCount     int       `json:"buyCount"`
	LastTradeAt  time.Time `json:"lastTradeAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type TradeUpdate struct {
	Event  pumpdev.Event
	Metric TradeMetric
}

type WatchStats struct {
	ActiveMints   int            `json:"activeMints"`
	WatcherCounts map[string]int `json:"watcherCounts"`
}

type subscriptionChange struct {
	method string
	mint   string
}

type TradeTracker struct {
	wsURL  string
	apiKey string
	logger zerolog.Logger

	changeReq chan subscriptionChange

	mu            sync.RWMutex
	watcherCounts map[string]int
	metrics       map[string]TradeMetric
}

func NewTradeTracker(wsURL, apiKey string, logger zerolog.Logger) *TradeTracker {
	return &TradeTracker{
		wsURL:         strings.TrimSpace(wsURL),
		apiKey:        strings.TrimSpace(apiKey),
		logger:        logger.With().Str("component", "pumpportal.trade_tracker").Logger(),
		changeReq:     make(chan subscriptionChange, 128),
		watcherCounts: make(map[string]int),
		metrics:       make(map[string]TradeMetric),
	}
}

func (t *TradeTracker) Enabled() bool {
	return t.wsURL != "" && t.apiKey != ""
}

func (t *TradeTracker) AddWatch(mint string) {
	mint = strings.TrimSpace(mint)
	if mint == "" || !t.Enabled() {
		return
	}

	t.mu.Lock()
	t.watcherCounts[mint]++
	count := t.watcherCounts[mint]
	t.mu.Unlock()
	if count > 1 {
		return
	}

	select {
	case t.changeReq <- subscriptionChange{method: "subscribeTokenTrade", mint: mint}:
	default:
		t.logger.Warn().Str("mint", mint).Msg("pumpportal trade track queue full")
	}
}

func (t *TradeTracker) RemoveWatch(mint string) {
	mint = strings.TrimSpace(mint)
	if mint == "" || !t.Enabled() {
		return
	}

	t.mu.Lock()
	count, ok := t.watcherCounts[mint]
	if !ok {
		t.mu.Unlock()
		return
	}
	count--
	if count <= 0 {
		delete(t.watcherCounts, mint)
		delete(t.metrics, mint)
	} else {
		t.watcherCounts[mint] = count
	}
	t.mu.Unlock()

	if count > 0 {
		return
	}

	select {
	case t.changeReq <- subscriptionChange{method: "unsubscribeTokenTrade", mint: mint}:
	default:
		t.logger.Warn().Str("mint", mint).Msg("pumpportal trade untrack queue full")
	}
}

func (t *TradeTracker) Snapshot(mint string) (TradeMetric, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	metric, ok := t.metrics[mint]
	return metric, ok
}

func (t *TradeTracker) Stats() WatchStats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	counts := make(map[string]int, len(t.watcherCounts))
	for mint, count := range t.watcherCounts {
		counts[mint] = count
	}
	return WatchStats{ActiveMints: len(counts), WatcherCounts: counts}
}

func (t *TradeTracker) Connect(ctx context.Context) (<-chan TradeUpdate, <-chan error) {
	out := make(chan TradeUpdate)
	errCh := make(chan error, 16)

	go func() {
		defer close(out)
		defer close(errCh)

		if !t.Enabled() {
			return
		}

		retryDelay := time.Second
		for {
			if ctx.Err() != nil {
				return
			}

			conn, err := t.dial()
			if err != nil {
				pushErr(errCh, err)
				if !waitRetry(ctx, retryDelay) {
					return
				}
				retryDelay = nextBackoff(retryDelay)
				continue
			}

			if err := t.consume(ctx, conn, out); err != nil && ctx.Err() == nil {
				pushErr(errCh, err)
				t.logger.Warn().Err(err).Msg("pumpportal trade tracker disconnected; reconnecting")
			}
			_ = conn.Close()

			if !waitRetry(ctx, retryDelay) {
				return
			}
			retryDelay = nextBackoff(retryDelay)
		}
	}()

	return out, errCh
}

func (t *TradeTracker) dial() (*websocket.Conn, error) {
	u, err := url.Parse(t.wsURL)
	if err != nil {
		return nil, fmt.Errorf("parse pumpportal websocket url: %w", err)
	}
	q := u.Query()
	q.Set("api-key", t.apiKey)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial pumpportal trade websocket: %w", err)
	}
	return conn, nil
}

func (t *TradeTracker) consume(ctx context.Context, conn *websocket.Conn, out chan<- TradeUpdate) error {
	if err := conn.SetReadDeadline(time.Now().Add(70 * time.Second)); err != nil {
		return err
	}
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	writerErr := make(chan error, 1)
	writerCtx, stopWriter := context.WithCancel(ctx)
	defer stopWriter()

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()

		for _, mint := range t.listTrackedMints() {
			if err := writeTradeSubscription(conn, "subscribeTokenTrade", mint); err != nil {
				writerErr <- err
				return
			}
		}

		for {
			select {
			case <-writerCtx.Done():
				return
			case change := <-t.changeReq:
				if err := writeTradeSubscription(conn, change.method, change.mint); err != nil {
					writerErr <- err
					return
				}
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second)); err != nil {
					writerErr <- err
					return
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-writerErr:
			return err
		default:
		}

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read pumpportal trade websocket message: %w", err)
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		update, ok := t.consumeTradePayload(payload)
		if !ok {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- update:
		}
	}
}

func (t *TradeTracker) consumeTradePayload(payload []byte) (TradeUpdate, bool) {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return TradeUpdate{}, false
	}

	event, metric, ok := t.extractTradeMetric(decoded)
	if !ok {
		return TradeUpdate{}, false
	}

	t.mu.Lock()
	current := t.metrics[metric.Mint]
	current.Mint = metric.Mint
	current.BuyVolumeSOL += metric.BuyVolumeSOL
	current.BuyCount += metric.BuyCount
	if metric.LastTradeAt.After(current.LastTradeAt) {
		current.LastTradeAt = metric.LastTradeAt
	}
	current.UpdatedAt = time.Now().UTC()
	t.metrics[metric.Mint] = current
	t.mu.Unlock()

	return TradeUpdate{Event: event, Metric: current}, true
}

func (t *TradeTracker) extractTradeMetric(node any) (pumpdev.Event, TradeMetric, bool) {
	m, ok := node.(map[string]any)
	if !ok || len(m) == 0 {
		return pumpdev.Event{}, TradeMetric{}, false
	}
	if msgType, _ := m["type"].(string); msgType == "connected" || msgType == "subscribed" {
		return pumpdev.Event{}, TradeMetric{}, false
	}
	mint := firstString(m, "mint", "tokenMint", "token", "ca", "address")
	if mint == "" {
		return pumpdev.Event{}, TradeMetric{}, false
	}
	txType := strings.ToLower(firstString(m, "txType", "tx_type", "type", "eventType", "event_type"))
	if txType != "buy" {
		return pumpdev.Event{}, TradeMetric{}, false
	}
	volume := firstFloat(m, "solAmount", "sol_amount", "amountSOL", "amount_sol", "volumeSOL", "volume")
	if volume <= 0 {
		return pumpdev.Event{}, TradeMetric{}, false
	}
	at := firstTime(m, "timestamp", "time", "ts", "createdAt", "created_at")
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return pumpdev.Event{
		Mint:          mint,
		Name:          firstString(m, "name", "tokenName", "token_name"),
		Symbol:        firstString(m, "symbol", "ticker", "tokenSymbol", "token_symbol"),
		VolumeSOL:     volume,
		MarketCapSOL:  firstFloat(m, "marketCapSol", "market_cap_sol", "marketCapSOL", "marketCap", "market_cap"),
		TraderAddress: firstString(m, "traderPublicKey", "trader", "wallet", "user", "userPublicKey"),
		Program:       "pumpportal",
		TxType:        txType,
		Signature:     firstString(m, "signature", "sig", "txSignature", "tx_signature"),
		Timestamp:     at,
	}, TradeMetric{Mint: mint, BuyVolumeSOL: volume, BuyCount: 1, LastTradeAt: at}, true
}

func (t *TradeTracker) listTrackedMints() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.watcherCounts))
	for mint := range t.watcherCounts {
		out = append(out, mint)
	}
	return out
}

func writeTradeSubscription(conn *websocket.Conn, method string, mint string) error {
	payload, err := json.Marshal(map[string]any{"method": method, "keys": []string{mint}})
	if err != nil {
		return fmt.Errorf("marshal %s: %w", method, err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("send %s: %w", method, err)
	}
	return nil
}
