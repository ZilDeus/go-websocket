package main

import (
	"fmt"
)

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
