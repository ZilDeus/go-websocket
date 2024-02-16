package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"
)

var rooms map[string]*Room
var offers map[*Client]Offer
var canidates map[string][]string
var pool *Pool

type Pool struct {
	Register   chan *Client
	Unregister chan *Client
	Clients    map[*Client]bool
	Broadcast  chan Message
	Created    chan *Room
	Deleted    chan string
}

type Room struct {
	Id    string
	Name  string
	Users []*Client
}
type RoomJson struct {
	Id    string   `json:"id"`
	Name  string   `json:"name"`
	Users []string `json:"users"`
}

func GenerateSSEResponses(w http.ResponseWriter, r *http.Request) {
	for {
		select {
		case id := <-pool.Deleted:
			fmt.Printf("sending delete event, id %s\n", id)
			response, err := formatServerSentEvent("delete", id)
			if err != nil {
				fmt.Println(err)
			}
			fmt.Fprintf(w, response)
			w.(http.Flusher).Flush()
			break
		case room := <-pool.Created:
			fmt.Printf("sending create event, room %s\n", room.Info())
			response, err := formatServerSentEvent("create", room.ToJson())
			if err != nil {
				fmt.Println(err)
			}
			fmt.Fprintf(w, response)
			w.(http.Flusher).Flush()
			break
		case <-r.Context().Done():
			fmt.Println("client disconnected")
			return
		}
	}
}
func formatServerSentEvent(event string, data any) (string, error) {
	m := map[string]any{
		"data": data,
	}

	buff := bytes.NewBuffer([]byte{})

	encoder := json.NewEncoder(buff)

	err := encoder.Encode(m)
	if err != nil {
		return "", err
	}

	sb := strings.Builder{}

	sb.WriteString(fmt.Sprintf("event: %s\n", event))
	sb.WriteString(fmt.Sprintf("data: %v\n\n", buff.String()))

	return sb.String(), nil
}

func (r *Room) ToJson() RoomJson {
	users := make([]string, len(r.Users))
	for i, user := range r.Users {
		users[i] = user.Username
	}
	return RoomJson{Id: r.Id, Name: r.Name, Users: users}
}

func (r *Room) Info() string {
	return fmt.Sprintf("%s (%s) clients:%d", r.Name, r.Id, len(r.Users))
}

func (r *Room) AddClient(c *Client) error {
	for _, client := range r.Users {
		if client == c {
			return errors.New(fmt.Sprintf("client %s already in room %s (%s)", c.Username, r.Name, r.Id))
		}
	}
	r.Users = append(r.Users, c)
	return nil
}

func (r *Room) RemoveClient(c *Client) error {
	if r == nil {
		return errors.New(fmt.Sprint("room already nil"))
	}
	for i, client := range r.Users {
		if client == c {
			if len(r.Users) == 1 {
				fmt.Printf("DELETING room %s(%s) as it's now empty\n", r.Name, r.Id)
				delete(rooms, r.Id)
				pool.Deleted <- r.Id
				fmt.Println("deleted")
			} else {
				r.Users[i] = r.Users[len(r.Users)-1]
				r.Users = r.Users[:len(r.Users)-1]
			}
			return nil
		}
	}
	return errors.New(fmt.Sprintf("client %s not in room %s (%s)", c.Username, r.Name, r.Id))
}

func (r *Room) GetClientByName(username string) (*Client, error) {
	for _, c := range r.Users {
		if c.Username == username {
			return c, nil
		}
	}
	return nil, errors.New(fmt.Sprintf("client with %s doesn't exsist", username))
}

func NewPool() *Pool {
	return &Pool{
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Clients:    make(map[*Client]bool),
		Broadcast:  make(chan Message),
		Created:    make(chan *Room),
		Deleted:    make(chan string),
	}
}

func (p *Pool) Start() {
	for {
		select {
		case client := <-p.Register:
			p.Clients[client] = true
			fmt.Println("size of connection pool", len(p.Clients))
			room := rooms[client.RoomId]
			if room == nil {
				fmt.Println("room:", client.RoomId, " doesn't exsist....")
				continue
			}
			err := room.AddClient(client)
			if err != nil {
				fmt.Println(err)
				continue
			}
			fmt.Println("client", client.Username, " joined room ", room.Info())
			ann := Announcement{Sender: client.Username, Event: "room-update", Data: room.ToJson()}
			for _, c := range room.Users {
				if err := c.Conn.WriteJSON(ann); err != nil {
					fmt.Println(err)
				}
			}
			break
		case client := <-p.Unregister:
			delete(p.Clients, client)
			delete(canidates, client.Username)
			delete(offers, client)
			room := rooms[client.RoomId]
			if room == nil {
				fmt.Println("room:", client.RoomId, " doesn't exsist....")
				continue
			}
			fmt.Println("Size of Connection Pool: ", len(p.Clients))
			fmt.Println("client", client.Username, " left room ", room.Info())
			err := rooms[client.RoomId].RemoveClient(client)
			if err != nil {
				fmt.Println(err)
				continue
			}
			if room := rooms[client.RoomId]; room != nil {
				ann := Announcement{Sender: client.Username, Event: "room-update", Data: room.ToJson()}
				for _, c := range room.Users {
					if err := c.Conn.WriteJSON(ann); err != nil {
						fmt.Println(err)
					}
				}
			}
			break
		case msg := <-p.Broadcast:
			fmt.Println("Sending message to all clients in room", rooms[msg.RoomId].Info())
			ann := Announcement{Sender: msg.Sender, Event: "message", Data: msg}
			for _, client := range rooms[msg.RoomId].Users {
				if err := client.Conn.WriteJSON(ann); err != nil {
					fmt.Println(err)
					return
				}
			}
		}
	}
}

type Client struct {
	Username string `json:"username"`
	RoomId   string `json:"room"`
	Conn     *websocket.Conn
	Pool     *Pool
}

type Announcement struct {
	Sender string `json:"sender"`
	Event  string `json:"event"`
	Data   any    `json:"data"`
}
type Offer struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}
type Message struct {
	RoomId string `json:"room"`
	Body   string `json:"body"`
	Sender string `json:"sender"`
}

func ParseMessage(ann *Announcement) Message {
	data := ann.Data.(map[string]interface{})
	msg := Message{}
	msg.Body = data["body"].(string)
	msg.Sender = data["sender"].(string)
	msg.RoomId = data["room"].(string)
	return msg
}
func ParseCanidate(ann *Announcement) string {
	data, err := json.Marshal(ann.Data)
	if err != nil {
		fmt.Println(err)
	}
	return string(data)
}
func ParseOffer(ann *Announcement) Offer {
	data := ann.Data.(map[string]interface{})
	offer := Offer{}
	offer.Type = data["type"].(string)
	offer.SDP = data["sdp"].(string)
	return offer
}
func (c *Client) Read() {
	defer func() {
		c.Pool.Unregister <- c
		c.Conn.Close()
	}()
	for {
		ann := Announcement{}
		err := c.Conn.ReadJSON(&ann)
		if err != nil {
			fmt.Println(err)
			return
		}
		if ann.Event == "" {
			return
		}
		fmt.Printf("client %s sent an announcement %s\n", c.Username, ann.Event)
		switch ann.Event {
		case "message":
			msg := ParseMessage(&ann)
			fmt.Printf("recived msg %s from client %s\n", msg.Body, msg.Sender)
			c.Pool.Broadcast <- msg
			break
		case "candidates":
			json := ann.Data.(map[string]interface{})
			from := json["from"].(string)
			to := json["to"].(string)
			fmt.Printf("recived candidates from %s to %s\n", from, to)
			client, err := rooms[c.RoomId].GetClientByName(to)
			if err != nil {
				fmt.Println(err)
			}
			client.Conn.WriteJSON(Announcement{Event: "candidates", Sender: from, Data: ann.Data})
			fmt.Printf("sent candidates to %s from %s\n", to, from)
			break
		case "offer":
			json := ann.Data.(map[string]interface{})
			from := json["from"].(string)
			to := json["to"].(string)
			fmt.Printf("recived offer from %s to %s\n", from, to)
			client, err := rooms[c.RoomId].GetClientByName(to)
			if err != nil {
				fmt.Println(err)
			}
			client.Conn.WriteJSON(Announcement{Event: "offer", Sender: from, Data: ann.Data})
			break
		case "load":
			from := ann.Sender
			room := rooms[c.RoomId]
			fmt.Printf("recived load event from %s\n", from)
			if len(room.Users) == 1 {
				fmt.Printf("not enogh client in room %s", room.Info())
				continue
			}
			for _, client := range room.Users {
				if c.Username == client.Username {
					continue
				}
				client.Conn.WriteJSON(Announcement{Event: "load", Sender: from, Data: ann.Data})
			}
			break
		case "play":
			from := ann.Sender
			room := rooms[c.RoomId]
			fmt.Printf("recived play event from %s\n", from)
			if len(room.Users) == 1 {
				fmt.Printf("not enogh client in room %s", room.Info())
				continue
			}
			for _, client := range room.Users {
				if c.Username == client.Username {
					continue
				}
				client.Conn.WriteJSON(Announcement{Event: "play", Sender: from, Data: ann.Data})
			}
			break
		case "pause":
			from := ann.Sender
			room := rooms[c.RoomId]
			fmt.Printf("recived pause event from %s\n", from)
			if len(room.Users) == 1 {
				fmt.Printf("not enogh client in room %s", room.Info())
				continue
			}
			for _, client := range room.Users {
				if c.Username == client.Username {
					continue
				}
				client.Conn.WriteJSON(Announcement{Event: "pause", Sender: from, Data: ann.Data})
			}
			break
		case "answer":
			json := ann.Data.(map[string]interface{})
			from := json["from"].(string)
			to := json["to"].(string)
			fmt.Printf("recived answer from %s to %s\n", from, to)
			client, err := rooms[c.RoomId].GetClientByName(to)
			if err != nil {
				fmt.Println(err)
			}
			client.Conn.WriteJSON(Announcement{Event: "answer", Sender: to, Data: ann.Data})
			break
		}
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
	tempC := Client{}
	err := client.Conn.ReadJSON(&tempC)
	if err != nil {
		fmt.Println(err)
	}
	client.Username = tempC.Username
	client.RoomId = tempC.RoomId
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

const charset = "abcdefghijklmnopqrstuvwxyz" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateID() string {
	seededRand := rand.New(
		rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 5)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}
func deleteEmptyRooms() {
	roomsToDelete := make([]string, 0)
	for _, r := range rooms {
		if len(r.Users) <= 0 {
			roomsToDelete = append(roomsToDelete, r.Id)
		}
	}
	for _, r := range roomsToDelete {
		delete(rooms, r)
	}
}
func setupRoutes() {
	router := mux.NewRouter()
	cors := cors.AllowAll()
	server := cors.Handler(router)
	go pool.Start()
	router.HandleFunc("/rooms_sse", func(w http.ResponseWriter, r *http.Request) {
		_, isSupported := w.(http.Flusher)
		if !isSupported {
			fmt.Println("server sent events are not supported on your browser :(")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		GenerateSSEResponses(w, r)
	})
	router.HandleFunc("/room/{id}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		if id == "" {
			fmt.Printf("id is undefined\n")
			return
		}
		fmt.Printf("checking if room with id %s exsist\n", id)
		if rooms[id] == nil {
			fmt.Printf("room with id %s not found\n", id)
			return
		}
		json, _ := json.Marshal(rooms[id].ToJson())
		fmt.Fprintf(w, string(json))
	}).Methods("GET")
	router.HandleFunc("/active_rooms", func(w http.ResponseWriter, r *http.Request) {
		deleteEmptyRooms()
		temprooms := make([]RoomJson, 0)
		for _, r := range rooms {
			temprooms = append(temprooms, r.ToJson())
		}
		if len(temprooms) == 0 {
			for c := range pool.Clients {
				delete(pool.Clients, c)
			}
		}
		type ActiveRoomsJson struct {
			Rooms []RoomJson `json:"rooms"`
		}
		fmt.Printf("%d rooms were found\n", len(temprooms))
		j, _ := json.Marshal(&ActiveRoomsJson{Rooms: temprooms})
		fmt.Fprint(w, string(j))
	}).Methods("GET")
	router.HandleFunc("/create_room", func(w http.ResponseWriter, r *http.Request) {
		var temp struct {
			Name string `json:"name"`
		}
		err := json.NewDecoder(r.Body).Decode(&temp)
		if err != nil {
			fmt.Println("could not create room", err)
		}
		var clients []*Client
		id := generateID()
		if err != nil {
			fmt.Println(err)
		}
		room := &Room{
			Id:    id,
			Name:  temp.Name,
			Users: clients,
		}
		rooms[id] = room
		fmt.Printf("room %s ID:%s created succussfully\n", room.Name, room.Id)
		roomJson, _ := json.Marshal(room.ToJson())
		pool.Created <- room
		fmt.Fprint(w, string(roomJson))
	}).Methods("POST")
	router.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(pool, w, r)
	})
	err := http.ListenAndServeTLS(":8080", "test.crt", "test.key", server)
	if err != nil {
		fmt.Println(err)
	}
}

func main() {
	fmt.Println("Chat App v0.02")
	rooms = make(map[string]*Room)
	offers = make(map[*Client]Offer)
	canidates = make(map[string][]string)
	pool = NewPool()
	setupRoutes()
}
