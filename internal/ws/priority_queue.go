package ws

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

type Priority int

const (
	PriorityP1Critical Priority = iota + 1
	PriorityP2High
	PriorityP3Normal
	PriorityP4Low
)

type Message struct {
	Priority   Priority
	Personal   bool
	Payload    []byte
	CreatedAt  time.Time
	RetryCount int
	MaxRetries int
}

// PriorityQueue enforces per-client message ordering with personal bypass.
// Personal messages bypass general queues to protect user-specific latency even
// under global spike storms.
type PriorityQueue struct {
	mu          sync.Mutex
	notEmpty    *sync.Cond
	closed      bool
	qP1         *list.List
	qP2         *list.List
	qP3         *list.List
	qP4         *list.List
	personalQ   *list.List
	retryBuffer *list.List
	retryCap    int
}

func NewPriorityQueue(retryCap int) *PriorityQueue {
	if retryCap < 0 {
		retryCap = 0
	}
	pq := &PriorityQueue{
		qP1:         list.New(),
		qP2:         list.New(),
		qP3:         list.New(),
		qP4:         list.New(),
		personalQ:   list.New(),
		retryBuffer: list.New(),
		retryCap:    retryCap,
	}
	pq.notEmpty = sync.NewCond(&pq.mu)
	return pq
}

func (q *PriorityQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.notEmpty.Broadcast()
}

func (q *PriorityQueue) Enqueue(msg Message) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("queue closed")
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	if msg.Personal {
		q.personalQ.PushBack(msg)
		q.notEmpty.Signal()
		return nil
	}

	switch msg.Priority {
	case PriorityP1Critical:
		q.qP1.PushBack(msg)
	case PriorityP2High:
		q.qP2.PushBack(msg)
	case PriorityP3Normal:
		q.qP3.PushBack(msg)
	default:
		q.qP4.PushBack(msg)
	}
	q.notEmpty.Signal()
	return nil
}

func (q *PriorityQueue) RetryP1(msg Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	msg.RetryCount++
	if msg.MaxRetries > 0 && msg.RetryCount > msg.MaxRetries {
		return
	}
	if q.retryCap > 0 && q.retryBuffer.Len() >= q.retryCap {
		q.retryBuffer.Remove(q.retryBuffer.Front())
	}
	q.retryBuffer.PushBack(msg)
	q.notEmpty.Signal()
}

func (q *PriorityQueue) Dequeue(ctx context.Context) (Message, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for {
		if q.closed {
			return Message{}, errors.New("queue closed")
		}
		if msg, ok := q.popNext(); ok {
			return msg, nil
		}
		if ctx.Err() != nil {
			return Message{}, ctx.Err()
		}

		waitDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				q.notEmpty.Broadcast()
			case <-waitDone:
			}
		}()
		q.notEmpty.Wait()
		close(waitDone)
	}
}

func (q *PriorityQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.personalQ.Len() + q.retryBuffer.Len() + q.qP1.Len() + q.qP2.Len() + q.qP3.Len() + q.qP4.Len()
}

func (q *PriorityQueue) popNext() (Message, bool) {
	for _, lst := range []*list.List{q.personalQ, q.retryBuffer, q.qP1, q.qP2, q.qP3, q.qP4} {
		if lst.Len() == 0 {
			continue
		}
		front := lst.Front()
		msg := front.Value.(Message)
		lst.Remove(front)
		return msg, true
	}
	return Message{}, false
}
