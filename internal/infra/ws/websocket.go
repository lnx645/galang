// Package ws provides WebSocket support for Garurda.
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message represents a WebSocket message.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	ID      string          `json:"id,omitempty"`
}

// HandlerFunc is the function signature for WebSocket message handlers.
type HandlerFunc func(*Conn, *Message)

// Conn wraps a WebSocket connection.
type Conn struct {
	*websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	send      chan []byte
	rooms     map[string]bool
	mu        sync.RWMutex
	id        string
	userData interface{}
}

// Hub manages WebSocket connections and rooms.
type Hub struct {
	clients    map[*Conn]bool
	rooms      map[string]map[*Conn]bool
	broadcast  chan []byte
	register   chan *Conn
	unregister chan *Conn
	handlers   map[string]HandlerFunc
	mu         sync.RWMutex
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Conn]bool),
		rooms:      make(map[string]map[*Conn]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Conn, 256),
		unregister: make(chan *Conn, 256),
		handlers:   make(map[string]HandlerFunc),
	}
}

// Handle registers a message handler.
func (h *Hub) Handle(msgType string, handler HandlerFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[msgType] = handler
}

// Run starts the hub's event loop.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.closeRooms()
				close(client.send)
			}
		case msg := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- msg:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg []byte) {
	h.broadcast <- msg
}

// BroadcastToRoom sends a message to all clients in a room.
func (h *Hub) BroadcastToRoom(room string, msg []byte) {
	h.mu.RLock()
	clients := h.rooms[room]
	h.mu.RUnlock()

	for client := range clients {
		select {
		case client.send <- msg:
		default:
			close(client.send)
		}
	}
}

// HandleMessage processes a message from a client.
func (h *Hub) HandleMessage(conn *Conn, msg *Message) {
	h.mu.RLock()
	handler, ok := h.handlers[msg.Type]
	h.mu.RUnlock()

	if ok {
		handler(conn, msg)
	}
}

// Register registers a client with the hub.
func (h *Hub) Register(conn *Conn) {
	h.register <- conn
}

// Unregister unregisters a client from the hub.
func (h *Hub) Unregister(conn *Conn) {
	h.unregister <- conn
}

// ServeWS handles WebSocket upgrade requests.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	upgrader := Upgrader
	conn, err := upgrader.Upgrade(w, nil, nil)
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		Conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		send:    make(chan []byte, 256),
		rooms:   make(map[string]bool),
		id:      fmt.Sprintf("%p", conn),
	}

	h.register <- c

	// Start write pump
	go c.writePump()

	// Read pump
	c.readPump(h)
}

// Upgrader is the default WebSocket upgrader.
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// writePump pumps messages from the hub to the WebSocket connection.
func (c *Conn) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(msg)
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// readPump reads messages from the WebSocket connection.
func (c *Conn) readPump(hub *Hub) {
	defer func() {
		hub.unregister <- c
		c.Close()
	}()

	c.SetReadLimit(512)
	c.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.SetPongHandler(func(string) error {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Log error
			}
			break
		}

		var msgData Message
		if err := json.Unmarshal(msg, &msgData); err != nil {
			continue
		}

		hub.HandleMessage(c, &msgData)
	}
}

// Send sends a message to the connection.
func (c *Conn) Send(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case c.send <- data:
		return nil
	default:
		return fmt.Errorf("send buffer full")
	}
}

// JoinRoom adds the connection to a room.
func (c *Conn) JoinRoom(room string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rooms[room] = true
}

// LeaveRoom removes the connection from a room.
func (c *Conn) LeaveRoom(room string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.rooms, room)
}

// closeRooms removes the connection from all rooms.
func (c *Conn) closeRooms() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for range c.rooms {
		// Would notify hub to remove from room
	}
	c.rooms = make(map[string]bool)
}

// SetUserData sets user data on the connection.
func (c *Conn) SetUserData(data interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userData = data
}

// UserData returns user data associated with the connection.
func (c *Conn) UserData() interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.userData
}

// ID returns the connection ID.
func (c *Conn) ID() string {
	return c.id
}

// Context returns the connection's context.
func (c *Conn) Context() context.Context {
	return c.ctx
}

// ServerWS handles WebSocket upgrade for a hub.
func ServerWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	upgrader := Upgrader
	conn, err := upgrader.Upgrade(w, nil, nil)
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		Conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		send:    make(chan []byte, 256),
		rooms:   make(map[string]bool),
		id:      fmt.Sprintf("%p", conn),
	}

	hub.register <- c

	go c.writePump()
	c.readPump(hub)
}