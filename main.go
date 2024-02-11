package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

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

type Room struct {
	Id    string
	Name  string
	Users []*Client
}
type RoomJson struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	UsersCount int    `json:"users"`
}

func (r *Room) ToJson() RoomJson {
	return RoomJson{Id: r.Id, Name: r.Name, UsersCount: len(r.Users)}
}
func (r *Room) Info() string {
	return fmt.Sprintf("%s (%s) users:%d", r.Name, r.Id, len(r.Users))
}
func (r *Room) AddClient(c *Client) error {
	for _, client := range r.Users {
		if client == c {
			return errors.New(fmt.Sprintf("user %s already in room %s (%s)", c.Username, r.Name, r.Id))
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
				fmt.Println("deleted")
			} else {
				r.Users[i] = r.Users[len(r.Users)-1]
				r.Users = r.Users[:len(r.Users)-1]
			}
			return nil
		}
	}
	return errors.New(fmt.Sprintf("user %s not in room %s (%s)", c.Username, r.Name, r.Id))
}
func NewPool() *Pool {
	return &Pool{
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Clients:    make(map[*Client]bool),
		Broadcast:  make(chan Message),
	}
}

var rooms map[string]*Room
var offers map[*Client]Offer
var canidates map[string][]string

//var activeClients []*Client

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
			fmt.Println("user", client.Username, " joined room ", room.Info())
			ann := Announcement{Sender: client.Username, Event: "user-joined", Data: room.ToJson()}
			for _, c := range room.Users {
				if err := c.Conn.WriteJSON(ann); err != nil {
					fmt.Println(err)
				}
			}
			ann = Announcement{Event: "offer-found"}
			for offerer, offer := range offers {
				if offerer.RoomId != client.RoomId {
					continue
				}
				ann.Data = offer
				ann.Sender = offerer.Username
				if err := client.Conn.WriteJSON(ann); err != nil {
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
			fmt.Println("user", client.Username, " left room ", room.Info())
			err := rooms[client.RoomId].RemoveClient(client)
			if err != nil {
				fmt.Println(err)
				continue
			}
			if room := rooms[client.RoomId]; room != nil {
				ann := Announcement{Sender: client.Username, Event: "user-left", Data: room.ToJson()}
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
		fmt.Printf("user %s sent an announcement %s\n", c.Username, ann.Event)
		switch ann.Event {
		case "message":
			msg := ParseMessage(&ann)
			fmt.Printf("recived msg %s from client %s\n", msg.Body, msg.Sender)
			c.Pool.Broadcast <- msg
			break
		case "offer-send":
			offer := ParseOffer(&ann)
			fmt.Printf("set client %s offer\n", c.Username)
			offers[c] = offer
			for _, roomC := range rooms[c.RoomId].Users {
				if roomC.Username == c.Username {
					continue
				}
				ann = Announcement{Event: "offer-recv", Sender: c.Username, Data: offer}
				if err := roomC.Conn.WriteJSON(ann); err != nil {
					fmt.Println(err)
				}
			}
			break
		case "canidate-send":
			canidate := ParseCanidate(&ann)
			if canidates[c.Username] == nil {
				canidates[c.Username] = make([]string, 0)
			}
			canidates[c.Username] = append(canidates[c.Username], canidate)
			fmt.Printf("set client %s canidate\n", c.Username)
			fmt.Printf("client %s canidates size %d\n", c.Username, len(canidates[c.Username]))
			break
		case "answer-send":
			other := ann.Sender
			offer := ParseOffer(&ann)
			fmt.Printf("client %s wants to answer clients %s call\n", c.Username, other)
			otherCanidates := make([]string, 0)
			for _, canidate := range canidates[other] {
				otherCanidates = append(otherCanidates, canidate)
			}
			ann = Announcement{Event: "answer-recv", Sender: other, Data: otherCanidates}
			if err := c.Conn.WriteJSON(ann); err != nil {
				fmt.Println(err)
			}
			for _, roomC := range rooms[c.RoomId].Users {
				if roomC.Username == other {
					ann = Announcement{Event: "answer", Sender: c.Username, Data: offer}
					if err := roomC.Conn.WriteJSON(ann); err != nil {
						fmt.Println(err)
					}
					break
				}
			}
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

//	func RemoveClientByUsername(username string) error {
//		var index int = -1
//		for i, c := range activeClients {
//			if c.Username == username {
//				index = i
//			}
//		}
//		if index == -1 {
//			return errors.New(fmt.Sprintf("client by username %s not found", username))
//		}
//		activeClients[index] = activeClients[len(activeClients)-1]
//		activeClients = activeClients[:len(activeClients)-1]
//		return nil
//	}
//
//	func GetClientByUsername(username string) (*Client, error) {
//		for _, c := range activeClients {
//			if c.Username == username {
//				return c, nil
//			}
//		}
//		return nil, errors.New(fmt.Sprintf("client by username %s not found", username))
//	}
func generateID() string {
	seededRand := rand.New(
		rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 5)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}
func setupRoutes() {
	router := mux.NewRouter()
	cors := cors.AllowAll()
	server := cors.Handler(router)
	pool := NewPool()
	go pool.Start()
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
		temprooms := make([]RoomJson, 0)
		for _, r := range rooms {
			temprooms = append(temprooms, r.ToJson())
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
		fmt.Fprint(w, string(roomJson))
	}).Methods("POST")
	router.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(pool, w, r)
	})
	http.ListenAndServe(":8080", server)
}

func main() {
	fmt.Println("Chat App v0.01")
	rooms = make(map[string]*Room)
	offers = make(map[*Client]Offer)
	canidates = make(map[string][]string)
	setupRoutes()
}
