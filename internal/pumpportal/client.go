package pumpportal

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/rs/zerolog"
)

const (
	StreamCreated  = "created"
	StreamMigrated = "migrated"
)

type Event struct {
	Stream        string    `json:"stream"`
	Mint          string    `json:"mint"`
	Name          string    `json:"name,omitempty"`
	Symbol        string    `json:"symbol,omitempty"`
	URI           string    `json:"uri,omitempty"`
	Pool          string    `json:"pool,omitempty"`
	IsMayhemMode  bool      `json:"isMayhemMode"`
	TxType        string    `json:"txType,omitempty"`
	Signature     string    `json:"signature,omitempty"`
	MarketCapSOL  float64   `json:"marketCapSOL,omitempty"`
	InitialBuySOL float64   `json:"initialBuySOL,omitempty"`
	DexID         string    `json:"dexId,omitempty"`
	PairAddress   string    `json:"pairAddress,omitempty"`
	PriceUSD      float64   `json:"priceUsd,omitempty"`
	PriceNative   float64   `json:"priceNative,omitempty"`
	MarketCapUSD  float64   `json:"marketCapUsd,omitempty"`
	LiquidityUSD  float64   `json:"liquidityUsd,omitempty"`
	FDV           float64   `json:"fdv,omitempty"`
	Volume5mUSD   float64   `json:"volume5mUsd,omitempty"`
	Volume1hUSD   float64   `json:"volume1hUsd,omitempty"`
	Buys5m        int       `json:"buys5m,omitempty"`
	Sells5m       int       `json:"sells5m,omitempty"`
	PairCreatedAt time.Time `json:"pairCreatedAt,omitempty"`
	ImageURL      string    `json:"imageUrl,omitempty"`
	WebsiteURL    string    `json:"websiteUrl,omitempty"`
	SocialHandle  string    `json:"socialHandle,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	RawPayload    string    `json:"rawPayload,omitempty"`
}

type Client struct {
	wsURL                string
	apiKey               string
	migrationCapturePath string
	logger               zerolog.Logger
	captureMu            sync.Mutex
	captureDone          bool
}

func NewClient(wsURL, apiKey, migrationCapturePath string, logger zerolog.Logger) *Client {
	return &Client{
		wsURL:                strings.TrimSpace(wsURL),
		apiKey:               strings.TrimSpace(apiKey),
		migrationCapturePath: strings.TrimSpace(migrationCapturePath),
		logger:               logger.With().Str("component", "pumpportal.client").Logger(),
	}
}

func (c *Client) Enabled() bool {
	return c.wsURL != "" && c.apiKey != ""
}

func (c *Client) Connect(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errCh := make(chan error, 16)

	go func() {
		defer close(events)
		defer close(errCh)

		if !c.Enabled() {
			return
		}

		retryDelay := time.Second
		for {
			if ctx.Err() != nil {
				return
			}

			conn, err := c.dial()
			if err != nil {
				pushErr(errCh, err)
				if !waitRetry(ctx, retryDelay) {
					return
				}
				retryDelay = nextBackoff(retryDelay)
				continue
			}

			c.logger.Info().Str("url", c.wsURL).Msg("pumpportal websocket connected")
			retryDelay = time.Second

			if err := writeSubscription(conn, "subscribeNewToken"); err != nil {
				_ = conn.Close()
				pushErr(errCh, err)
				if !waitRetry(ctx, retryDelay) {
					return
				}
				continue
			}
			if err := writeSubscription(conn, "subscribeMigration"); err != nil {
				_ = conn.Close()
				pushErr(errCh, err)
				if !waitRetry(ctx, retryDelay) {
					return
				}
				continue
			}

			readErr := c.consume(ctx, conn, events)
			_ = conn.Close()
			if readErr != nil && ctx.Err() == nil {
				pushErr(errCh, readErr)
				c.logger.Warn().Err(readErr).Msg("pumpportal websocket disconnected; reconnecting")
			}

			if !waitRetry(ctx, retryDelay) {
				return
			}
			retryDelay = nextBackoff(retryDelay)
		}
	}()

	return events, errCh
}

func (c *Client) dial() (*websocket.Conn, error) {
	u, err := url.Parse(c.wsURL)
	if err != nil {
		return nil, fmt.Errorf("parse pumpportal websocket url: %w", err)
	}
	q := u.Query()
	q.Set("api-key", c.apiKey)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial pumpportal websocket: %w", err)
	}
	return conn, nil
}

func writeSubscription(conn *websocket.Conn, method string) error {
	payload, err := json.Marshal(map[string]any{"method": method})
	if err != nil {
		return fmt.Errorf("marshal %s subscription: %w", method, err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("send %s subscription: %w", method, err)
	}
	return nil
}

func (c *Client) consume(ctx context.Context, conn *websocket.Conn, out chan<- Event) error {
	if err := conn.SetReadDeadline(time.Now().Add(70 * time.Second)); err != nil {
		return err
	}
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read pumpportal websocket message: %w", err)
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		events := decodeEvents(payload)
		for _, ev := range events {
			c.captureMigrationPayload(ev)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- ev:
			}
		}
	}
}

func (c *Client) captureMigrationPayload(ev Event) {
	if ev.Stream != StreamMigrated || c.migrationCapturePath == "" || ev.RawPayload == "" {
		return
	}

	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	if c.captureDone {
		return
	}

	path := filepath.Clean(c.migrationCapturePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		c.logger.Warn().Err(err).Str("path", path).Msg("failed to create migration capture directory")
		return
	}
	if err := os.WriteFile(path, []byte(ev.RawPayload+"\n"), 0o644); err != nil {
		c.logger.Warn().Err(err).Str("path", path).Msg("failed to write migration capture payload")
		return
	}
	c.captureDone = true
	c.logger.Info().Str("path", path).Msg("captured first raw pumpportal migration payload")
}

func decodeEvents(payload []byte) []Event {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil
	}

	if items, ok := decoded.([]any); ok {
		out := make([]Event, 0, len(items))
		for _, item := range items {
			out = append(out, extractEvent(item, payload)...)
		}
		return out
	}

	return extractEvent(decoded, payload)
}

func extractEvent(node any, payload []byte) []Event {
	m, ok := node.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}

	if msgType, _ := m["type"].(string); msgType == "connected" || msgType == "subscribed" {
		return nil
	}

	ev, ok := eventFromMap(m)
	if !ok {
		return nil
	}
	ev.RawPayload = string(payload)
	return []Event{ev}
}

func eventFromMap(m map[string]any) (Event, bool) {
	mint := firstString(m, "mint", "tokenMint", "token", "ca", "address")
	if mint == "" {
		return Event{}, false
	}

	txType := firstString(m, "txType", "tx_type", "eventType", "event_type", "type")
	stream := classifyStream(txType, m)
	if stream == "" {
		return Event{}, false
	}

	timestamp := firstTime(m, "timestamp", "time", "ts", "createdAt", "created_at")
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	return Event{
		Stream:        stream,
		Mint:          mint,
		Name:          firstString(m, "name", "tokenName", "token_name"),
		Symbol:        firstString(m, "symbol", "ticker", "tokenSymbol", "token_symbol"),
		URI:           firstString(m, "uri", "metadataUri", "metadata_uri"),
		Pool:          firstString(m, "pool"),
		IsMayhemMode:  firstBool(m, "is_mayhem_mode", "isMayhemMode"),
		TxType:        txType,
		Signature:     firstString(m, "signature", "sig", "txSignature", "tx_signature"),
		MarketCapSOL:  firstFloat(m, "marketCapSol", "market_cap_sol", "marketCapSOL", "marketCap", "market_cap"),
		InitialBuySOL: math.Abs(firstFloat(m, "initialBuy", "initial_buy", "solAmount", "sol_amount")),
		Timestamp:     timestamp,
	}, true
}

func classifyStream(txType string, m map[string]any) string {
	lowerType := strings.ToLower(strings.TrimSpace(txType))
	switch {
	case strings.Contains(lowerType, "migrat"):
		return StreamMigrated
	case strings.Contains(lowerType, "create"):
		return StreamCreated
	}

	if firstString(m, "poolAddress", "migrationPool", "liquidityPool") != "" {
		return StreamMigrated
	}
	if firstString(m, "bondingCurveKey", "uri", "name", "symbol") != "" {
		return StreamCreated
	}
	return ""
}

type RecentBuffer struct {
	mu      sync.RWMutex
	maxSize int
	items   map[string][]Event
}

func NewRecentBuffer(maxSize int) *RecentBuffer {
	if maxSize <= 0 {
		maxSize = 64
	}
	return &RecentBuffer{maxSize: maxSize, items: map[string][]Event{StreamCreated: {}, StreamMigrated: {}}}
}

func (b *RecentBuffer) Add(ev Event) {
	if ev.Stream == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	current := append([]Event{ev}, b.items[ev.Stream]...)
	if len(current) > b.maxSize {
		current = current[:b.maxSize]
	}
	b.items[ev.Stream] = current
}

func (b *RecentBuffer) List(stream string, limit int) []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()
	current := b.items[stream]
	if limit <= 0 || limit > len(current) {
		limit = len(current)
	}
	out := make([]Event, limit)
	copy(out, current[:limit])
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			s := strings.TrimSpace(t)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func firstFloat(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return t
		case float32:
			return float64(t)
		case int:
			return float64(t)
		case int64:
			return float64(t)
		case json.Number:
			f, err := t.Float64()
			if err == nil {
				return f
			}
		case string:
			parsed, err := json.Number(strings.TrimSpace(t)).Float64()
			if err == nil {
				return parsed
			}
		}
	}
	return 0
}

func firstBool(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case bool:
			return t
		case string:
			return strings.EqualFold(strings.TrimSpace(t), "true")
		}
	}
	return false
}

func firstTime(m map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(t)); err == nil {
				return parsed
			}
		case float64:
			return time.Unix(int64(t), 0).UTC()
		case int64:
			return time.Unix(t, 0).UTC()
		}
	}
	return time.Time{}
}

func pushErr(out chan<- error, err error) {
	select {
	case out <- err:
	default:
	}
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > 20*time.Second {
		return 20 * time.Second
	}
	return next
}
