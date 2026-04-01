package ws

import (
        "encoding/json"
        "log"
        "net/http"
        "sync"

        "github.com/gin-gonic/gin"
        "github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
        ReadBufferSize:  1024,
        WriteBufferSize: 4096,
        CheckOrigin:     func(r *http.Request) bool { return true },
}

type LiveEvent struct {
        Type      string      `json:"type"`
        CompanyID string      `json:"companyId,omitempty"`
        Payload   interface{} `json:"payload"`
}

type Client struct {
        hub      *Hub
        conn     *websocket.Conn
        send     chan []byte
        mu       sync.Mutex
}

type Hub struct {
        clients    map[*Client]bool
        broadcast  chan []byte
        register   chan *Client
        unregister chan *Client
        mu         sync.RWMutex
}

var GlobalHub = NewHub()

func NewHub() *Hub {
        return &Hub{
                clients:    make(map[*Client]bool),
                broadcast:  make(chan []byte, 256),
                register:   make(chan *Client),
                unregister: make(chan *Client),
        }
}

func (h *Hub) Run() {
        for {
                select {
                case client := <-h.register:
                        h.mu.Lock()
                        h.clients[client] = true
                        h.mu.Unlock()
                case client := <-h.unregister:
                        h.mu.Lock()
                        if _, ok := h.clients[client]; ok {
                                delete(h.clients, client)
                                close(client.send)
                        }
                        h.mu.Unlock()
                case message := <-h.broadcast:
                        h.mu.RLock()
                        for client := range h.clients {
                                select {
                                case client.send <- message:
                                default:
                                        close(client.send)
                                        delete(h.clients, client)
                                }
                        }
                        h.mu.RUnlock()
                }
        }
}

func (h *Hub) Publish(event LiveEvent) {
        data, err := json.Marshal(event)
        if err != nil {
                log.Printf("[ws] marshal error: %v", err)
                return
        }
        select {
        case h.broadcast <- data:
        default:
        }
}

func (c *Client) writePump() {
        defer func() {
                c.conn.Close()
        }()
        for msg := range c.send {
                c.mu.Lock()
                err := c.conn.WriteMessage(websocket.TextMessage, msg)
                c.mu.Unlock()
                if err != nil {
                        return
                }
        }
}

func (c *Client) readPump() {
        defer func() {
                c.hub.unregister <- c
                c.conn.Close()
        }()
        for {
                _, _, err := c.conn.ReadMessage()
                if err != nil {
                        break
                }
        }
}

func ServeWs(hub *Hub) gin.HandlerFunc {
        return func(c *gin.Context) {
                conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
                if err != nil {
                        log.Printf("[ws] upgrade error: %v", err)
                        return
                }
                client := &Client{
                        hub:  hub,
                        conn: conn,
                        send: make(chan []byte, 256),
                }
                hub.register <- client

                go client.writePump()
                go client.readPump()
        }
}
