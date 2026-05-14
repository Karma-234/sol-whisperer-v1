package pumpdev
package pumpdev

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
)

// Event is the normalized shape emitted by PumpDev integration.
// A single normalized contract keeps downstream components (volume/listener/ws)
// decoupled from source-specific payload changes.
type Event struct {
	Mint          string
	Symbol        string
	VolumeSOL     float64
	TraderAddress string
	Program       string
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
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		if c.wsURL == "" {
			errs <- errors.New("pumpdev ws url is empty")
			return
		}

		// TODO: connect to wss://pumpdev.io/ws and subscribe to pump.fun/raydium logs.
		// A heartbeat ticker keeps the goroutine alive until cancellation, giving a
		// deterministic lifecycle for orchestration tests.
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.logger.Debug().Msg("pumpdev skeleton heartbeat")
			}
		}
	}()

	return events, errs
}
