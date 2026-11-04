package tenant

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Event is a record change broadcast to realtime subscribers and webhooks.
type Event struct {
	Event      string         `json:"event"` // create|update|delete
	DB         string         `json:"db"`
	Collection string         `json:"collection"`
	Record     map[string]any `json:"record"`
}

// Hub fans record events out to SSE subscribers, keyed by database+collection.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

func NewHub() *Hub { return &Hub{subs: make(map[string]map[chan Event]struct{})} }

func hubKey(db, col string) string { return db + "\x00" + col }

func (h *Hub) SubCount(db, col string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[hubKey(db, col)])
}

func (h *Hub) Subscribe(db, col string) (chan Event, func()) {
	ch := make(chan Event, 16)
	key := hubKey(db, col)
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = make(map[chan Event]struct{})
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if s, ok := h.subs[key]; ok {
			delete(s, ch)
			close(ch)
			if len(s) == 0 {
				delete(h.subs, key)
			}
		}
	}
}

func (h *Hub) Broadcast(e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[hubKey(e.DB, e.Collection)] {
		select {
		case ch <- e:
		default: // slow subscriber: drop, keep the hub non-blocking
		}
	}
}

// Webhooks are fire-and-forget POSTs of the event JSON to configured URLs.
var webhookClient = &http.Client{Timeout: 5 * time.Second}

// FireWebhooks POSTs e to every url, each in its own goroutine.
func FireWebhooks(urls []string, e Event) {
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	for _, u := range urls {
		go func(url string) {
			req, err := http.NewRequest("POST", url, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-SakuData-Event", e.Event)
			resp, err := webhookClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
		}(u)
	}
}

// HooksFor returns webhook URLs registered for an action on a collection.
func HooksFor(c *Collection, action string) []string {
	if c.Hooks == nil {
		return nil
	}
	return c.Hooks[action]
}
