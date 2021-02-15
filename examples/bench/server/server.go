package main

import (
	"fmt"

	"github.com/lesismal/nbio"
)

func onData(c *nbio.Conn, data []byte) {
	c.Write(append([]byte{}, data...))
}

func main() {
	g := nbio.NewGopher(nbio.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	})

	g.OnData(onData)

	err := g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	g.Wait()
}
