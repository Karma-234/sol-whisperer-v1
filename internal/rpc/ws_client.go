package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/rs/zerolog"

	"github.karma-234/sol-whisperer-v1/internal/pumpdev"
)

// ProgramAddresses are the mainnet Solana programs we monitor for buy/sell activity.
var ProgramAddresses = []string{
	"39azUhRZyoDi8vrvt32H5CstNaYvkMqvty75nuk92sBP", // pump.fun bonding curve
	"CPMMoo8L3F4NbTegBCKVNunggL7H1ZpdTHKxQB5qKP1C", // Raydium CPMM
	"675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8", // Raydium AMM v4
}

// WSClient manages subscription to Solana RPC WebSocket logsSubscribe filtered by program address.
type WSClient struct {
	tierManager *TierManager
	logger      zerolog.Logger
}

// NewWSClient creates an RPC WebSocket client bound to Tier A.
func NewWSClient(tierManager *TierManager, logger zerolog.Logger) *WSClient {
	return &WSClient{
		tierManager: tierManager,
		logger:      logger.With().Str("component", "rpc.ws_client").Logger(),
	}
}

// Connect dials the Tier A WebSocket endpoint and subscribes to logsSubscribe.
// It returns channels for normalized Events and errors.
// The channels are closed when the context is canceled or a fatal error occurs.
func (c *WSClient) Connect(ctx context.Context) (<-chan pumpdev.Event, <-chan error) {
	events := make(chan pumpdev.Event)
	errs := make(chan error, 16)

	go func() {
		defer close(events)
		defer close(errs)

		retryDelay := time.Second
		subscriptionID := ""

		for {
			if ctx.Err() != nil {
				return
			}

			conn, dialErr := c.dial(ctx)
			if dialErr != nil {
				pushErr(errs, dialErr)
				if !waitRetry(ctx, retryDelay) {
					return
				}
				retryDelay = nextBackoff(retryDelay)
				continue
			}

			c.logger.Info().Msg("rpc websocket connected")
			retryDelay = time.Second

			// Subscribe to logsSubscribe with program mentions
			subID, subErr := c.subscribe(conn, ProgramAddresses)
			if subErr != nil {
				c.logger.Warn().Err(subErr).Msg("rpc logsSubscribe failed; reconnecting")
				_ = conn.Close()
				if !waitRetry(ctx, retryDelay) {
					return
				}
				retryDelay = nextBackoff(retryDelay)
				continue
			}
			subscriptionID = subID

			// Consume messages until error or context cancel
			readErr := c.consume(ctx, conn, events, subscriptionID)
			_ = conn.Close()
			subscriptionID = ""

			if readErr != nil && !errors.Is(readErr, context.Canceled) {
				pushErr(errs, readErr)
				c.logger.Warn().Err(readErr).Msg("rpc websocket disconnected; reconnecting")
			}

			if !waitRetry(ctx, retryDelay) {
				return
			}
			retryDelay = nextBackoff(retryDelay)
		}
	}()

	return events, errs
}

func (c *WSClient) dial(ctx context.Context) (*websocket.Conn, error) {
	url, err := c.tierManager.NextWS(TierA)
	if err != nil {
		return nil, fmt.Errorf("get tier a ws endpoint: %w", err)
	}

	// Replace https:// with wss:// and http:// with ws://
	url = strings.TrimSpace(url)
	if strings.HasPrefix(url, "https://") {
		url = "wss://" + url[8:]
	} else if strings.HasPrefix(url, "http://") {
		url = "ws://" + url[7:]
	} else if !strings.HasPrefix(url, "wss://") && !strings.HasPrefix(url, "ws://") {
		url = "wss://" + url
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, dialErr := dialer.DialContext(ctx, url, nil)
	if dialErr != nil {
		return nil, fmt.Errorf("dial rpc websocket: %w", dialErr)
	}
	return conn, nil
}

// subscribe sends logsSubscribe request with program address filters.
// Returns the subscription ID on success.
func (c *WSClient) subscribe(conn *websocket.Conn, programs []string) (string, error) {
	if len(programs) == 0 {
		return "", errors.New("no program addresses to subscribe")
	}

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "logsSubscribe",
		"params": []any{
			map[string]any{
				"mentions": programs,
			},
			map[string]string{
				"commitment": "finalized",
			},
		},
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal logsSubscribe: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return "", fmt.Errorf("send logsSubscribe: %w", err)
	}

	// Read subscription response to extract subscription ID
	_, respPayload, err := conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("read subscription response: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(respPayload, &resp); err != nil {
		return "", fmt.Errorf("parse subscription response: %w", err)
	}

	if result, ok := resp["result"].(float64); ok {
		return fmt.Sprintf("%.0f", result), nil
	}
	if result, ok := resp["result"].(string); ok {
		return result, nil
	}

	return "", errors.New("no subscription id in response")
}

// consume reads logsNotification messages and emits normalized Events.
func (c *WSClient) consume(ctx context.Context, conn *websocket.Conn, out chan<- pumpdev.Event, subscriptionID string) error {
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

	for {
		select {
		case <-ctx.Done():
			stopPing()
			<-pingDone
			return ctx.Err()
		default:
		}

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			stopPing()
			<-pingDone
			return fmt.Errorf("read rpc websocket message: %w", err)
		}

		if msgType != websocket.TextMessage {
			continue
		}

		events := c.decodeLogsNotification(payload)
		for _, ev := range events {
			if ev.Timestamp.IsZero() {
				ev.Timestamp = time.Now().UTC()
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

// decodeLogsNotification parses RPC logsNotification and extracts events.
// Response format:
//
//	{
//	  "jsonrpc": "2.0",
//	  "method": "logsNotification",
//	  "params": {
//	    "result": {
//	      "context": {"slot": 12345},
//	      "value": {
//	        "signature": "...",
//	        "err": null,
//	        "logs": ["..."],
//	        "commitment": "finalized"
//	      }
//	    },
//	    "subscription": 0
//	  }
//	}
func (c *WSClient) decodeLogsNotification(payload []byte) []pumpdev.Event {
	var notification map[string]any
	if err := json.Unmarshal(payload, &notification); err != nil {
		return nil
	}

	// Check if this is a logsNotification
	method, ok := notification["method"].(string)
	if !ok || method != "logsNotification" {
		return nil // Skip non-notification messages (e.g., subscription confirm)
	}

	params, ok := notification["params"].(map[string]any)
	if !ok {
		return nil
	}

	result, ok := params["result"].(map[string]any)
	if !ok {
		return nil
	}

	value, ok := result["value"].(map[string]any)
	if !ok {
		return nil
	}

	// Extract signature and error status
	sig, ok := value["signature"].(string)
	if !ok || sig == "" {
		return nil
	}

	// Skip failed transactions
	if errVal, ok := value["err"]; ok && errVal != nil {
		return nil
	}

	// Extract logs
	logs, ok := value["logs"].([]any)
	if !ok || len(logs) == 0 {
		return nil
	}

	// Convert logs to strings
	logStrs := make([]string, 0, len(logs))
	for _, log := range logs {
		if s, ok := log.(string); ok {
			logStrs = append(logStrs, s)
		}
	}

	// Parse logs to extract program data and build event
	// This is a simplified parser that looks for token mints and volumes in logs.
	// For now, return a minimal event with the signature for dedup purposes.
	// More sophisticated parsing of SPL token program logs would happen here.
	ev := pumpdev.Event{
		Signature: sig,
		Timestamp: time.Now().UTC(),
	}

	// Try to extract basic info from logs
	ev = c.parseLogsForTokenInfo(ev, logStrs)

	// Only emit events that have extracted meaningful data
	if ev.Mint != "" {
		return []pumpdev.Event{ev}
	}

	return nil
}

// parseLogsForTokenInfo attempts to extract token mint and volume info from program logs.
// This is a placeholder for more sophisticated parsing based on program instruction patterns.
func (c *WSClient) parseLogsForTokenInfo(ev pumpdev.Event, logs []string) pumpdev.Event {
	// TODO: Implement SPL token program instruction parsing to extract:
	// - Token mint
	// - Volume (in lamports or token amount)
	// - Trader address (instruction signer)
	// For now, return minimal event with signature for dedup only.
	return ev
}

// Helper functions (same as in pumpdev/client.go)

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
