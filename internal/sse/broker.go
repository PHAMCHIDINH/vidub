package sse

import (
	"bufio"
	"fmt"
	"sync"

	"github.com/gofiber/fiber/v2"
)

type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan string]struct{}
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[string]map[chan string]struct{})}
}

func (b *Broker) Subscribe(jobID string) chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[jobID] == nil {
		b.subscribers[jobID] = make(map[chan string]struct{})
	}
	b.subscribers[jobID][ch] = struct{}{}
	return ch
}

func (b *Broker) Unsubscribe(jobID string, ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subs, ok := b.subscribers[jobID]; ok {
		delete(subs, ch)
		close(ch)
		if len(subs) == 0 {
			delete(b.subscribers, jobID)
		}
	}
}

func (b *Broker) Publish(jobID, event, data string) {
	line := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers[jobID] {
		select {
		case ch <- line:
		default:
		}
	}
}

func (b *Broker) SSEHandler(c *fiber.Ctx) error {
	jobID := c.Params("id")

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	doneCh := c.Context().Done()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ch := b.Subscribe(jobID)
		defer b.Unsubscribe(jobID, ch)

		for {
			select {
			case <-doneCh:
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprint(w, msg)
				w.Flush()
			}
		}
	})

	return nil
}
