package main

import (
	"fmt"
	"log"
	"net"

	"github.com/lesismal/nbio"
)

func main() {
	g := nbio.NewGopher(nbio.Config{})
	g.OnOpen(func(c *nbio.Conn) {
		log.Println("OnOpen:", c.RemoteAddr().String())
	})

	g.OnData(func(c *nbio.Conn, data []byte) {
		c.Write(append([]byte{}, data...))
	})

	err := g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer g.Stop()

	ln, err := net.Listen("tcp", "localhost:8888")
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept failed:", err)
			continue
		}
		g.AddConn(conn)
	}
}
