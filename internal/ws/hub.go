package ws

import (
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type ClientID string

type Client struct {
	ID            ClientID
	Queue         *PriorityQueue
	RateLimitPerS int
	lastRefill    time.Time
	tokens        int
	mu            sync.Mutex
}

type Hub struct {
	mu       sync.RWMutex
	clients  map[ClientID]*Client
	retryCap int
	logger   zerolog.Logger
}

func NewHub(logger zerolog.Logger, retryCap int) *Hub {
	return &Hub{
		clients:  make(map[ClientID]*Client),
		retryCap: retryCap,
		logger:   logger.With().Str("component", "ws.hub").Logger(),
	}
}

func (h *Hub) AddClient(id ClientID, rateLimitPerS int) *Client {
	if rateLimitPerS <= 0 {
		rateLimitPerS = 20
	}
	c := &Client{
		ID:            id,
		Queue:         NewPriorityQueue(h.retryCap),
		RateLimitPerS: rateLimitPerS,
		lastRefill:    time.Now().UTC(),
		tokens:        rateLimitPerS,
	}
	h.mu.Lock()
	h.clients[id] = c
	h.mu.Unlock()
	return c
}

func (h *Hub) RemoveClient(id ClientID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[id]; ok {
		c.Queue.Close()
		delete(h.clients, id)
	}
}

func (h *Hub) EnqueueForClient(id ClientID, msg Message) bool {
	h.mu.RLock()
	client, ok := h.clients[id]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	if !client.allowSend() {
		if msg.Priority == PriorityP1Critical || msg.Personal {
			client.Queue.RetryP1(msg)
		}
		return false
	}
	if err := client.Queue.Enqueue(msg); err != nil {
		return false
	}
	return true
}

func (h *Hub) Broadcast(msg Message) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	delivered := 0
	for _, c := range h.clients {
		if !c.allowSend() {
			if msg.Priority == PriorityP1Critical || msg.Personal {
				c.Queue.RetryP1(msg)
			}
			continue
		}
		if err := c.Queue.Enqueue(msg); err == nil {
			delivered++
		}
	}
	return delivered
}

func (h *Hub) Stats() map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	queued := 0
	for _, c := range h.clients {
		queued += c.Queue.Len()
	}
	return map[string]any{
		"clients": h.clientCountUnsafe(),
		"queued":  queued,
	}
}

func (h *Hub) clientCountUnsafe() int {
	return len(h.clients)
}

func (c *Client) allowSend() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	elapsed := now.Sub(c.lastRefill)
	if elapsed >= time.Second {
		refills := int(elapsed / time.Second)
		c.tokens += refills * c.RateLimitPerS
		if c.tokens > c.RateLimitPerS {
			c.tokens = c.RateLimitPerS
		}
		c.lastRefill = now
	}
	if c.tokens <= 0 {
		return false
	}
	c.tokens--
	return true
}
