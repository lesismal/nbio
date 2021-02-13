package main

import (
	"fmt"

	"github.com/lesismal/nbio"
)

func main() {
	g := nbio.NewGopher(nbio.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
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

	g.Wait()
}
