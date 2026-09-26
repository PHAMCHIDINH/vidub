package sse

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// heartbeatInterval is how often an idle stream writes a comment line.
// A failed write is the only way fasthttp tells us the client has gone.
const heartbeatInterval = 15 * time.Second

type event struct {
	id   uint64
	wire string // fully formatted SSE frame
}

// Broker fans out pipeline events per job and keeps each job's history,
// so a client that connects late (or reconnects) still sees every event.
type Broker struct {
	mu          sync.Mutex
	nextID      uint64 // global, so IDs never repeat across job re-runs
	history     map[string][]event
	subscribers map[string]map[chan event]struct{}
}

func NewBroker() *Broker {
	return &Broker{
		history:     make(map[string][]event),
		subscribers: make(map[string]map[chan event]struct{}),
	}
}

// Reset drops the stored history of a job. Call it before re-running a job.
func (b *Broker) Reset(jobID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.history, jobID)
}

// Subscribe registers a listener and returns the stored events newer than lastID.
func (b *Broker) Subscribe(jobID string, lastID uint64) (chan event, []event) {
	ch := make(chan event, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[jobID] == nil {
		b.subscribers[jobID] = make(map[chan event]struct{})
	}
	b.subscribers[jobID][ch] = struct{}{}

	var replay []event
	for _, ev := range b.history[jobID] {
		if ev.id > lastID {
			replay = append(replay, ev)
		}
	}
	return ch, replay
}

func (b *Broker) Unsubscribe(jobID string, ch chan event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removeLocked(jobID, ch)
}

func (b *Broker) removeLocked(jobID string, ch chan event) {
	subs := b.subscribers[jobID]
	if _, ok := subs[ch]; !ok {
		return // already removed
	}
	delete(subs, ch)
	close(ch)
	if len(subs) == 0 {
		delete(b.subscribers, jobID)
	}
}

func (b *Broker) Publish(jobID, name, data string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	ev := event{id: b.nextID, wire: formatEvent(b.nextID, name, data)}
	b.history[jobID] = append(b.history[jobID], ev)

	for ch := range b.subscribers[jobID] {
		select {
		case ch <- ev:
		default:
			// Slow client: drop its stream. The browser reconnects with
			// Last-Event-ID and gets the missed events from history.
			b.removeLocked(jobID, ch)
		}
	}
}

// formatEvent builds an SSE frame. Every line of data needs its own "data:"
// prefix, otherwise the browser silently drops everything after a newline.
func formatEvent(id uint64, name, data string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "id: %d\nevent: %s\n", id, name)
	for _, line := range strings.Split(data, "\n") {
		sb.WriteString("data: ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func (b *Broker) SSEHandler(c *fiber.Ctx) error {
	jobID := c.Params("id")
	lastID, _ := strconv.ParseUint(c.Get("Last-Event-ID"), 10, 64)

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	doneCh := c.Context().Done() // closed on server shutdown

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ch, replay := b.Subscribe(jobID, lastID)
		defer b.Unsubscribe(jobID, ch)

		for _, ev := range replay {
			w.WriteString(ev.wire)
		}
		if err := w.Flush(); err != nil {
			return
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()

		for {
			select {
			case <-doneCh:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				w.WriteString(ev.wire)
			case <-heartbeat.C:
				w.WriteString(": ping\n\n")
			}
			if err := w.Flush(); err != nil {
				return // client disconnected
			}
		}
	})

	return nil
}
