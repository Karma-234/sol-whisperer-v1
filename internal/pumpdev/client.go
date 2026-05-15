package pumpdev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/rs/zerolog"
)

// Event is the normalized shape emitted by PumpDev integration.
// A single normalized contract keeps downstream components (volume/listener/ws)
// decoupled from source-specific payload changes.
type Event struct {
	Mint          string
	Name          string
	Symbol        string
	VolumeSOL     float64
	MarketCapSOL  float64
	TraderAddress string
	Program       string
	TxType        string
	Signature     string
	Timestamp     time.Time
}

type Client struct {
	wsURL  string
	logger zerolog.Logger
}

func NewClient(wsURL string, logger zerolog.Logger) *Client {
	return &Client{
		wsURL:  wsURL,
		logger: logger.With().Str("component", "pumpdev.client").Logger(),
	}
}

func (c *Client) Connect(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errs := make(chan error, 16)

	go func() {
		defer close(events)
		defer close(errs)

		if c.wsURL == "" {
			errs <- errors.New("pumpdev ws url is empty")
			return
		}

		retryDelay := time.Second
		for {
			if ctx.Err() != nil {
				return
			}

			conn, dialErr := c.dial()
			if dialErr != nil {
				pushErr(errs, dialErr)
				if !waitRetry(ctx, retryDelay) {
					return
				}
				retryDelay = nextBackoff(retryDelay)
				continue
			}

			c.logger.Info().Str("url", c.wsURL).Msg("pumpdev websocket connected")
			retryDelay = time.Second

			if subErr := c.sendSubscriptions(conn); subErr != nil {
				c.logger.Warn().Err(subErr).Msg("pumpdev subscription send failed; continuing")
			}

			readErr := c.consume(ctx, conn, events)
			_ = conn.Close()
			if readErr != nil && !errors.Is(readErr, context.Canceled) {
				pushErr(errs, readErr)
				c.logger.Warn().Err(readErr).Msg("pumpdev websocket disconnected; reconnecting")
			}

			if !waitRetry(ctx, retryDelay) {
				return
			}
			retryDelay = nextBackoff(retryDelay)
		}
	}()

	return events, errs
}

func (c *Client) dial() (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(c.wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial pumpdev websocket: %w", err)
	}
	return conn, nil
}

func (c *Client) sendSubscriptions(conn *websocket.Conn) error {
	if err := writePumpDevSubscription(conn, "subscribeNewToken", nil); err != nil {
		return err
	}
	return nil
}

func writePumpDevSubscription(conn *websocket.Conn, method string, keys []string) error {
	msg := map[string]any{"method": method}
	if len(keys) > 0 {
		msg["keys"] = keys
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal subscription message: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("send %s subscription message: %w", method, err)
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
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		defer close(pingDone)
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			}
		}
	}()

	subscribedTrades := make(map[string]struct{})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			stopPing()
			<-pingDone
			return fmt.Errorf("read pumpdev websocket message: %w", err)
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		events := decodeEvents(payload)
		for _, ev := range events {
			if ev.Timestamp.IsZero() {
				ev.Timestamp = time.Now().UTC()
			}
			if ev.Mint != "" && strings.EqualFold(ev.TxType, "create") {
				if _, ok := subscribedTrades[ev.Mint]; !ok {
					if err := writePumpDevSubscription(conn, "subscribeTokenTrade", []string{ev.Mint}); err != nil {
						c.logger.Warn().Err(err).Str("mint", ev.Mint).Msg("pumpdev token trade subscription failed")
					} else {
						subscribedTrades[ev.Mint] = struct{}{}
						c.logger.Info().Str("mint", ev.Mint).Msg("subscribed to pumpdev token trades")
					}
				}
			}
			select {
			case <-ctx.Done():
				stopPing()
				<-pingDone
				return ctx.Err()
			case out <- ev:
			}
		}
	}
}

func decodeEvents(payload []byte) []Event {
	var anyJSON any
	if err := json.Unmarshal(payload, &anyJSON); err != nil {
		return nil
	}

	out := make([]Event, 0, 2)
	switch v := anyJSON.(type) {
	case []any:
		for _, item := range v {
			out = append(out, extractEvent(item)...)
		}
	default:
		out = append(out, extractEvent(v)...)
	}
	return out
}

func extractEvent(node any) []Event {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}

	if nested, ok := m["data"]; ok {
		if ev, ok := eventFromMap(asMap(nested)); ok {
			return []Event{ev}
		}
	}
	if nested, ok := m["event"]; ok {
		if ev, ok := eventFromMap(asMap(nested)); ok {
			return []Event{ev}
		}
	}
	if nested, ok := m["result"]; ok {
		if ev, ok := eventFromMap(asMap(nested)); ok {
			return []Event{ev}
		}
	}

	if ev, ok := eventFromMap(m); ok {
		return []Event{ev}
	}
	return nil
}

func eventFromMap(m map[string]any) (Event, bool) {
	if len(m) == 0 {
		return Event{}, false
	}
	mint := firstString(m, "mint", "tokenMint", "token_mint", "token", "ca", "address")
	if mint == "" {
		return Event{}, false
	}

	volume := firstFloat(m, "volumeSOL", "volume_sol", "volume", "amountSOL", "amount_sol", "amount", "solAmount", "sol_amount")
	if volume <= 0 {
		return Event{}, false
	}

	timestamp := firstTime(m, "timestamp", "time", "ts", "createdAt", "created_at")

	ev := Event{
		Mint:          mint,
		Name:          firstString(m, "name", "tokenName", "token_name"),
		Symbol:        firstString(m, "symbol", "ticker", "tokenSymbol", "token_symbol"),
		VolumeSOL:     math.Abs(volume),
		MarketCapSOL:  firstFloat(m, "marketCapSol", "market_cap_sol", "marketCapSOL", "marketCap", "market_cap"),
		TraderAddress: firstString(m, "trader", "wallet", "owner", "user", "signer", "traderPublicKey", "trader_public_key"),
		Program:       firstString(m, "program", "source", "market"),
		TxType:        firstString(m, "txType", "tx_type", "type", "eventType", "event_type"),
		Signature:     firstString(m, "sig", "signature", "txSignature", "tx_signature", "tx_sig"),
		Timestamp:     timestamp,
	}
	return ev, true
}

func asMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
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
		case fmt.Stringer:
			s := strings.TrimSpace(t.String())
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
			f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err == nil {
				return f
			}
		}
	}
	return 0
}

func firstTime(m map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			ts := strings.TrimSpace(t)
			if ts == "" {
				continue
			}
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				return parsed.UTC()
			}
			if unix, err := strconv.ParseInt(ts, 10, 64); err == nil {
				return unixToTime(unix)
			}
		case float64:
			return unixToTime(int64(t))
		case int64:
			return unixToTime(t)
		case int:
			return unixToTime(int64(t))
		}
	}
	return time.Time{}
}

func unixToTime(v int64) time.Time {
	// Some feeds publish milliseconds; normalize to seconds when value is too large.
	if v > 1_000_000_000_000 {
		v = v / 1000
	}
	return time.Unix(v, 0).UTC()
}

func waitRetry(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > 20*time.Second {
		return 20 * time.Second
	}
	return next
}

func pushErr(errs chan<- error, err error) {
	if err == nil {
		return
	}
	select {
	case errs <- err:
	default:
	}
}
