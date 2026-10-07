// Package sse provides Server-Sent Events support for Garurda.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Event represents an SSE event.
type Event struct {
	ID    string          `json:"id,omitempty"`
	Event string          `json:"event,omitempty"`
	Data  json.RawMessage `json:"data"`
	Retry int             `json:"retry,omitempty"`
}

// Client represents an SSE client connection.
type Client struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan *Event
	id     string
	mu     sync.Mutex
}

// NewClient creates a new SSE client.
func NewClient(ctx context.Context) *Client {
	ctx, cancel := context.WithCancel(ctx)
	return &Client{
		ctx:    ctx,
		cancel: cancel,
		ch:     make(chan *Event, 256),
		id:     fmt.Sprintf("%p", &struct{}{}),
	}
}

// Send sends an event to the client.
func (c *Client) Send(event *Event) error {
	select {
	case c.ch <- event:
		return nil
	case <-c.ctx.Done():
		return context.Canceled
	default:
		return fmt.Errorf("client buffer full")
	}
}

// Close closes the client connection.
func (c *Client) Close() {
	c.cancel()
	close(c.ch)
}

// ID returns the client ID.
func (c *Client) ID() string {
	return c.id
}

// Context returns the client's context.
func (c *Client) Context() context.Context {
	return c.ctx
}

// Channel returns the client's event channel.
func (c *Client) Channel() <-chan *Event {
	return c.ch
}

// Hub manages SSE clients.
type Hub struct {
	clients map[*Client]bool
	mu      sync.RWMutex
}

// NewHub creates a new SSE hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
	}
}

// Register registers a client with the hub.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(event *Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if err := client.Send(event); err != nil {
			h.mu.RUnlock()
			h.Unregister(client)
			h.mu.RLock()
		}
	}
}

// BroadcastEvent broadcasts a typed event to all clients.
func (h *Hub) BroadcastEvent(eventName string, data interface{}) error {
	data, err := json.Marshal(data)
	if err != nil {
		return err
	}

	event := &Event{
		Event: eventName,
		Data:  json.RawMessage(data.([]byte)),
	}
	h.Broadcast(event)
	return nil
}

// ServeSSE handles SSE connections.
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	client := NewClient(ctx)

	h.Register(client)
	defer func() {
		h.Unregister(client)
		client.Close()
		cancel()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"id\":\"%s\"}\n\n", client.ID())
	flusher.Flush()

	for {
		select {
		case event := <-client.ch:
			writeEvent(w, event)
			flusher.Flush()
		case <-client.ctx.Done():
			return
		case <-r.Context().Done():
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, event *Event) {
	if event.ID != "" {
		fmt.Fprintf(w, "id: %s\n", event.ID)
	}
	if event.Event != "" {
		fmt.Fprintf(w, "event: %s\n", event.Event)
	}
	if event.Data != nil {
		fmt.Fprintf(w, "data: %s\n", event.Data)
	}
	if event.Retry > 0 {
		fmt.Fprintf(w, "retry: %d\n", event.Retry)
	}
	fmt.Fprint(w, "\n")
}

// Writer is a helper for writing SSE events.
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewWriter creates a new SSE writer.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &Writer{w: w, flusher: flusher}, nil
}

// WriteEvent writes an event to the stream.
func (w *Writer) WriteEvent(event *Event) error {
	if event.ID != "" {
		fmt.Fprintf(w.w, "id: %s\n", event.ID)
	}
	if event.Event != "" {
		fmt.Fprintf(w.w, "event: %s\n", event.Event)
	}
	if event.Data != nil {
		fmt.Fprintf(w.w, "data: %s\n", event.Data)
	}
	if event.Retry > 0 {
		fmt.Fprintf(w.w, "retry: %d\n", event.Retry)
	}
	fmt.Fprint(w.w, "\n")
	w.flusher.Flush()
	return nil
}

// Close closes the writer.
func (w *Writer) Close() error {
	return nil
}