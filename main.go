package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"
)

type Pool struct {
	Register   chan *Client
	Unregister chan *Client
	Clients    map[*Client]bool
	Broadcast  chan Message
}

func NewPool() *Pool {
	return &Pool{
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Clients:    make(map[*Client]bool),
		Broadcast:  make(chan Message),
	}
}

func (p *Pool) Start() {
	for {
		select {
		case client := <-p.Register:
			p.Clients[client] = true
			fmt.Println("a new user joined")
			fmt.Println("size of connection pool", len(p.Clients))
			for c := range p.Clients {
				c.Conn.WriteJSON(Message{Type: 1, Body: "a new user joined "})
			}
			break
		case client := <-p.Unregister:
			{
				delete(p.Clients, client)
				fmt.Println("Size of Connection Pool: ", len(p.Clients))
				for c := range p.Clients {
					c.Conn.WriteJSON(Message{Type: 1, Body: "User Disconnected..."})
				}
				break
			}
		case msg := <-p.Broadcast:
			{
				fmt.Println("Sending message to all clients in Pool")
				for client := range p.Clients {
					if err := client.Conn.WriteJSON(msg); err != nil {
						fmt.Println(err)
						return
					}
				}
			}
		}
	}
}

type Client struct {
	ID   string
	Conn *websocket.Conn
	Pool *Pool
}

type Message struct {
	Type int    `json:"type"`
	Body string `json:"body"`
}

func (c *Client) Read() {
	defer func() {
		c.Pool.Unregister <- c
		c.Conn.Close()
	}()
	for {

		messageType, data, err := c.Conn.ReadMessage()
		if err != nil {
			fmt.Println(err)
			return
		}
		Message := Message{Type: messageType, Body: string(data)}
		fmt.Println("message recived", Message.Type, ":", Message.Body)
		c.Pool.Broadcast <- Message
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func serveWs(pool *Pool, w http.ResponseWriter, r *http.Request) {
	ws := CreateSocket(w, r)
	client := &Client{
		Conn: ws,
		Pool: pool,
	}
	client.Pool.Register <- client
	client.Read()
}
func CreateSocket(w http.ResponseWriter, r *http.Request) *websocket.Conn {
	fmt.Println(r.Host)
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
	}
	return ws
}

func setupRoutes() {
	mux := mux.NewRouter()
	cors := cors.AllowAll()
	server := cors.Handler(mux)
	pool := NewPool()
	go pool.Start()
	mux.HandleFunc("/active_rooms", func(w http.ResponseWriter, r *http.Request) {
		jsonresp, _ := json.Marshal(Message{Type: 1, Body: "hello there traverler"})
		fmt.Fprintf(w, string(jsonresp))
	}).Methods("GET")
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(pool, w, r)
	})
	http.ListenAndServe(":8080", server)
}

func main() {
	fmt.Println("Chat App v0.01")
	setupRoutes()
}
