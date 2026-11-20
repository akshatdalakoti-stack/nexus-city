package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"nexuscity/internal/metrics"
)

// Hub fans messages out to WebSocket clients. Each client has a bounded
// queue; a client that cannot keep up loses messages rather than slowing
// everyone else down.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	dropped atomic.Int64
	sent    atomic.Int64
	origins []string
}

type client struct {
	send chan []byte
}

// NewHub creates a hub accepting the given origin host patterns
// (e.g. "localhost:3000"); an empty list allows any origin.
func NewHub(origins []string) *Hub {
	return &Hub{clients: map[*client]struct{}{}, origins: origins}
}

// Clients returns the number of connected clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Broadcast queues msg for every client.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			h.dropped.Add(1)
		}
	}
}

// Serve upgrades the request to a WebSocket and streams messages until the
// client goes away. hello is sent first so a new client has current state.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, hello [][]byte) {
	opts := &websocket.AcceptOptions{OriginPatterns: h.origins}
	if len(h.origins) == 0 {
		opts.InsecureSkipVerify = true
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		slog.Warn("websocket accept failed", "err", err)
		return
	}
	defer conn.CloseNow()

	c := &client{send: make(chan []byte, 256)}
	for _, m := range hello {
		c.send <- m
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	metrics.WSClients.Set(float64(len(h.clients)))
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		metrics.WSClients.Set(float64(len(h.clients)))
		h.mu.Unlock()
	}()

	ctx := conn.CloseRead(r.Context()) // clients only listen; this detects disconnects
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
			h.sent.Add(1)
		}
	}
}

// wrap builds the {"type": ..., "data": ...} message sent to clients
// without re-encoding the already-serialised envelope.
func wrap(typ string, raw []byte) []byte {
	out := make([]byte, 0, len(raw)+len(typ)+20)
	out = append(out, `{"type":"`...)
	out = append(out, typ...)
	out = append(out, `","data":`...)
	out = append(out, raw...)
	return append(out, '}')
}
