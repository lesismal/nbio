package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/log"
)

func main() {
	var (
		ret    []byte
		buf    = make([]byte, 1024)
		addr   = "localhost:8888"
		ctx, _ = context.WithTimeout(context.Background(), time.Second)
	)

	log.SetLevel(log.LevelInfo)

	rand.Read(buf)

	g := nbio.NewGopher(nbio.Config{})

	done := make(chan int)
	g.OnData(func(c *nbio.Conn, data []byte) {
		ret = append(ret, data...)
		if len(ret) == len(buf) {
			if bytes.Equal(buf, ret) {
				close(done)
			}
		}
	})

	err := g.Start()
	if err != nil {
		fmt.Printf("Start failed: %v\n", err)
	}
	defer g.Stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Printf("Dial failed: %v\n", err)
	}
	g.AddConn(c)
	c.Write(buf)

	select {
	case <-ctx.Done():
		log.Error("timeout")
	case <-done:
		log.Info("success")
	}
}
