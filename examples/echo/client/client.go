package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/log"
)

func main() {
	var (
		ret         []byte
		buf         = make([]byte, 1024)
		addr        = "localhost:8888"
		ctx, cancel = context.WithCancel(context.Background())
	)

	log.SetLevel(log.LevelInfo)
	rand.Read(buf)

	g := nbio.NewGopher(nbio.Config{})
	g.OnData(func(c *nbio.Conn, data []byte) {
		ret = append(ret, data...)
		if len(ret) == len(buf) {
			if bytes.Equal(buf, ret) {
				cancel()
			}
		}
	})

	err := g.Start()
	if err != nil {
		fmt.Printf("Start failed: %v\n", err)
	}
	defer g.Stop()

	c, err := nbio.Dial("tcp", addr)
	if err != nil {
		fmt.Printf("Dial failed: %v\n", err)
	}
	g.AddConn(c)
	c.Write(buf)

	select {
	case <-ctx.Done():
		log.Info("success")
	case <-time.After(time.Second * 2):
		log.Error("timeout")
	}
}
