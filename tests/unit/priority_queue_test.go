package unit

import (
	"context"
	"testing"
	"time"

	"github.karma-234/sol-whisperer-v1/internal/ws"
)

func TestPriorityQueue_PersonalBypassAndPriorityOrder(t *testing.T) {
	q := ws.NewPriorityQueue(8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	mustEnqueue(t, q, ws.Message{Priority: ws.PriorityP3Normal, Payload: []byte("p3")})
	mustEnqueue(t, q, ws.Message{Priority: ws.PriorityP1Critical, Payload: []byte("p1")})
	mustEnqueue(t, q, ws.Message{Priority: ws.PriorityP4Low, Payload: []byte("p4")})
	mustEnqueue(t, q, ws.Message{Priority: ws.PriorityP2High, Payload: []byte("p2")})
	mustEnqueue(t, q, ws.Message{Priority: ws.PriorityP4Low, Personal: true, Payload: []byte("personal")})

	msg, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue personal: %v", err)
	}
	if string(msg.Payload) != "personal" {
		t.Fatalf("expected personal first, got %q", string(msg.Payload))
	}

	assertNextPayload(t, q, ctx, "p1")
	assertNextPayload(t, q, ctx, "p2")
	assertNextPayload(t, q, ctx, "p3")
	assertNextPayload(t, q, ctx, "p4")
}

func TestPriorityQueue_RetryP1Buffer(t *testing.T) {
	q := ws.NewPriorityQueue(2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	q.RetryP1(ws.Message{Priority: ws.PriorityP1Critical, Payload: []byte("a"), MaxRetries: 3})
	q.RetryP1(ws.Message{Priority: ws.PriorityP1Critical, Payload: []byte("b"), MaxRetries: 3})
	q.RetryP1(ws.Message{Priority: ws.PriorityP1Critical, Payload: []byte("c"), MaxRetries: 3})

	assertNextPayload(t, q, ctx, "b")
	assertNextPayload(t, q, ctx, "c")
}

func mustEnqueue(t *testing.T, q *ws.PriorityQueue, m ws.Message) {
	t.Helper()
	if err := q.Enqueue(m); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func assertNextPayload(t *testing.T, q *ws.PriorityQueue, ctx context.Context, expected string) {
	t.Helper()
	msg, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if string(msg.Payload) != expected {
		t.Fatalf("expected %q got %q", expected, string(msg.Payload))
	}
}
