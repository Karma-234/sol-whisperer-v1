package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	fiberws "github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog"
)

var connCounter uint64

func RegisterRoutes(app *fiber.App, hub *Hub, logger zerolog.Logger, resolveUserID func(string) (string, error)) {
	log := logger.With().Str("component", "ws.server").Logger()

	app.Use("/ws", func(c *fiber.Ctx) error {
		if fiberws.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return c.Status(fiber.StatusUpgradeRequired).JSON(fiber.Map{"error": "websocket upgrade required"})
	})

	app.Get("/ws/stream", fiberws.New(func(conn *fiberws.Conn) {
		if resolveUserID == nil {
			_ = conn.WriteJSON(fiber.Map{"type": "error", "message": "server auth resolver is not configured"})
			_ = conn.Close()
			return
		}

		initData := strings.TrimSpace(conn.Query("tgInitData"))
		if initData == "" {
			_ = conn.WriteJSON(fiber.Map{"type": "error", "message": "tgInitData query parameter is required"})
			_ = conn.Close()
			return
		}
		userID, authErr := resolveUserID(initData)
		if authErr != nil {
			_ = conn.WriteJSON(fiber.Map{"type": "error", "message": "telegram authentication failed"})
			_ = conn.Close()
			return
		}

		serveWSClient(conn, hub, log, userID)
	}))

	app.Get("/ws/public", fiberws.New(func(conn *fiberws.Conn) {
		serveWSClient(conn, hub, log, "")
	}))
}

func serveWSClient(conn *fiberws.Conn, hub *Hub, log zerolog.Logger, userID string) {
	clientPrefix := "public"
	if userID != "" {
		clientPrefix = userID
	}

	clientID := ClientID(fmt.Sprintf("%s-%d", clientPrefix, atomic.AddUint64(&connCounter, 1)))
	var client *Client
	if userID == "" {
		client = hub.AddClient(clientID, 60)
	} else {
		client = hub.AddClientForUser(clientID, userID, 60)
	}
	defer hub.RemoveClient(clientID)

	log.Info().Str("clientId", string(clientID)).Str("userId", userID).Msg("ws client connected")
	defer log.Info().Str("clientId", string(clientID)).Str("userId", userID).Msg("ws client disconnected")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var writeMu sync.Mutex
	writeJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}
	writePayload := func(payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(fiberws.TextMessage, payload)
	}

	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	connectedUser := userID
	if connectedUser == "" {
		connectedUser = "public"
	}
	_ = writeJSON(fiber.Map{"type": "connected", "clientId": clientID, "userId": connectedUser})

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			heartbeat, _ := json.Marshal(fiber.Map{
				"type":     "heartbeat",
				"priority": "P4",
				"ts":       time.Now().UTC(),
			})
			if err := writePayload(heartbeat); err != nil {
				return
			}
		default:
			dqCtx, dqCancel := context.WithTimeout(ctx, 3*time.Second)
			msg, err := client.Queue.Dequeue(dqCtx)
			dqCancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					continue
				}
				return
			}
			if err := writePayload(msg.Payload); err != nil {
				return
			}
		}
	}
}
