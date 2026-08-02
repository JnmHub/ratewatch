package syncer

import (
	"encoding/json"
	"sync"

	"ratewatch/internal/store"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[int64]map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{clients: map[int64]map[chan []byte]struct{}{}} }
func (h *Hub) Subscribe(uid int64) (chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	if h.clients[uid] == nil {
		h.clients[uid] = map[chan []byte]struct{}{}
	}
	h.clients[uid][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() { h.mu.Lock(); delete(h.clients[uid], ch); close(ch); h.mu.Unlock() }
}
func (h *Hub) Publish(v store.Event) {
	b, _ := json.Marshal(v)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients[v.UserID] {
		select {
		case ch <- b:
		default:
		}
	}
}
