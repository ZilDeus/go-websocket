// TODO FINISH THE ROOMS SYSTEM
// last thing i did was implement creating a room
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
	Id         string    `json:"id"`
	Name       string    `json:"name"`
	Users      []*Client `json:"_"`
	UsersCount int       `json:"users"`
}

func (r *Room) AddClient(c *Client) error {
	for _, client := range r.Users {
		if client == c {
			return errors.New(fmt.Sprintf("user %s already in room %s (%s)", c.Username, r.Name, r.Id))
		}
	}
	r.Users = append(r.Users, c)
	r.UsersCount = len(r.Users)
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
var activeClients []*Client

func (p *Pool) Start() {
	for {
		select {
		case client := <-p.Register:
			p.Clients[client] = true
			activeClients = append(activeClients, client)
			fmt.Println("size of connection pool", len(p.Clients))
			if rooms[client.RoomId] == nil {
				fmt.Println("the room you are tring to access doesn't exsist....")
				return
			}
			err := rooms[client.RoomId].AddClient(client)
			if err != nil {
				fmt.Println(err)
			}
			fmt.Println("user", client.Username, " joined room ", client.RoomId, "(", rooms[client.RoomId].Name, ")")
			room := rooms[client.RoomId]
			tempRome := Room{Name: room.Name, UsersCount: len(room.Users), Id: room.Id}
			roomJson, _ := json.Marshal(tempRome)
			msg := Message{Type: 1, Body: string(roomJson)}
			fmt.Printf("sending a message to all %d users of room %s(%s)", tempRome.UsersCount, tempRome.Name, tempRome.Id)
			for _, client := range rooms[client.RoomId].Users {
				if err := client.Conn.WriteJSON(msg); err != nil {
					fmt.Println(err)
					return
				}
			}
			break
		case client := <-p.Unregister:
			delete(p.Clients, client)
			RemoveClientByUsername(client.Username)
			fmt.Println("Size of Connection Pool: ", len(p.Clients))
			fmt.Printf("user %s leaving room %s(%s) usersCount %d\n", client.Username, rooms[client.RoomId].Name, rooms[client.RoomId].Id, len(rooms[client.RoomId].Users))
			err := rooms[client.RoomId].RemoveClient(client)
			if err != nil {
				fmt.Println(err)
				return
			}
			if room := rooms[client.RoomId]; room != nil {
				tempRome := Room{Name: room.Name, UsersCount: len(room.Users), Id: room.Id}
				roomJson, _ := json.Marshal(tempRome)
				msg := Message{Type: 1, Body: string(roomJson)}
				fmt.Printf("sending a message to all %d users of room %s(%s)", tempRome.UsersCount, tempRome.Name, tempRome.Id)
				for _, client := range rooms[client.RoomId].Users {
					if err := client.Conn.WriteJSON(msg); err != nil {
						fmt.Println(err)
						return
					}
				}
			}
			break
		case msg := <-p.Broadcast:
			fmt.Println("Sending message to all clients in room", msg.RoomId)
			msg.Type = 0
			for _, client := range rooms[msg.RoomId].Users {
				if err := client.Conn.WriteJSON(msg); err != nil {
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

type Message struct {
	Type   int    `json:"type"`
	RoomId string `json:"room"`
	Body   string `json:"body"`
}

func (c *Client) Read() {
	defer func() {
		c.Pool.Unregister <- c
		c.Conn.Close()
	}()
	for {
		msg := Message{}
		err := c.Conn.ReadJSON(&msg)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("message recived", msg.Body)
		c.Pool.Broadcast <- msg
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

func RemoveClientByUsername(username string) error {
	var index int = -1
	for i, c := range activeClients {
		if c.Username == username {
			index = i
		}
	}
	if index == -1 {
		return errors.New(fmt.Sprintf("client by username %s not found", username))
	}
	activeClients[index] = activeClients[len(activeClients)-1]
	activeClients = activeClients[:len(activeClients)-1]
	return nil
}
func GetClientByUsername(username string) (*Client, error) {
	for _, c := range activeClients {
		if c.Username == username {
			return c, nil
		}
	}
	return nil, errors.New(fmt.Sprintf("client by username %s not found", username))
}
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
	mux := mux.NewRouter()
	cors := cors.AllowAll()
	server := cors.Handler(mux)
	pool := NewPool()
	go pool.Start()
	mux.HandleFunc("/active_rooms", func(w http.ResponseWriter, r *http.Request) {
		temprooms := make([]Room, 0)
		for _, r := range rooms {
			temprooms = append(temprooms, Room{
				Name:       r.Name,
				Id:         r.Id,
				UsersCount: len(r.Users),
			})
		}
		type ActiveRoomsJson struct {
			Rooms []Room `json:"rooms"`
		}
		fmt.Printf("%d rooms were found\n", len(temprooms))
		j, _ := json.Marshal(&ActiveRoomsJson{Rooms: temprooms})
		fmt.Fprint(w, string(j))
	}).Methods("GET")
	//mux.HandleFunc("/leave_room", func(w http.ResponseWriter, r *http.Request) {
	//	var temp struct {
	//		Name string `json:"username"`
	//		Id   string `json:"id"`
	//	}
	//	err := json.NewDecoder(r.Body).Decode(&temp)
	//	if err != nil {
	//		fmt.Println("could not join room", err)
	//	}

	//	client, err := GetClientByUsername(temp.Name)
	//	if err != nil {
	//		fmt.Println(err)
	//	}

	//	room := rooms[temp.Id]
	//	if room == nil {
	//		fmt.Println("room with the id ", temp.Id, " not found")
	//		return
	//	}

	//	for i, client := range room.Users {
	//		if client.userName == temp.Name {
	//			room.Users[i] = room.Users[room.UsersCount-1]
	//			room.Users = room.Users[:room.UsersCount-1]
	//			room.UsersCount--
	//			fmt.Printf("user %s successfully left room %s users count %d\n", client.userName, room.Name, room.UsersCount)
	//			return
	//		}
	//	}
	//	fmt.Printf("user %s not in room %s (%s)\n", client.userName, room.Name, room.Id)
	//}).Methods("POST")
	//mux.HandleFunc("/join_room", func(w http.ResponseWriter, r *http.Request) {
	//	var temp struct {
	//		Name string `json:"username"`
	//		Id   string `json:"id"`
	//	}
	//	err := json.NewDecoder(r.Body).Decode(&temp)
	//	if err != nil {
	//		fmt.Println("could not join room", err)
	//	}

	//	client, err := GetClientByUsername(temp.Name)
	//	if err != nil {
	//		fmt.Println(err)
	//	}

	//	room := rooms[temp.Id]
	//	if room == nil {
	//		fmt.Println("room with the id ", temp.Id, " not found")
	//		return
	//	}

	//	for _, client := range room.Users {
	//		if client.userName == temp.Name {
	//			fmt.Printf("user %s already in room %s (%s)\n", client.userName, room.Name, room.Id)
	//		}
	//	}
	//	room.Users = append(room.Users, client)
	//	room.UsersCount++
	//	fmt.Printf("user %s successfully joind room %s users count %d\n", client.userName, room.Name, room.UsersCount)
	//	roomJson, _ := json.Marshal(Room{Name: room.Name, Id: room.Id, UsersCount: room.UsersCount})
	//	fmt.Fprint(w, string(roomJson))
	//}).Methods("POST")
	mux.HandleFunc("/create_room", func(w http.ResponseWriter, r *http.Request) {
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
			Id:         id,
			Name:       temp.Name,
			Users:      clients,
			UsersCount: 0,
		}
		rooms[id] = room
		fmt.Printf("room %s ID:%s\ncreated succussfully\n", room.Name, room.Id)
		roomJson, _ := json.Marshal(room)
		fmt.Fprint(w, string(roomJson))
	}).Methods("POST")
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(pool, w, r)
	})
	http.ListenAndServe(":8080", server)
}

func main() {
	fmt.Println("Chat App v0.01")
	rooms = make(map[string]*Room)
	activeClients = make([]*Client, 0)
	setupRoutes()
}
