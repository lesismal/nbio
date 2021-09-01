package main

import (
	"fmt"

	"github.com/lesismal/nbio"
)

// func onData(c *nbio.Conn, data []byte) {
// 	c.Write(append([]byte{}, data...))
// }

func main() {
	g := nbio.NewGopher(nbio.Config{
		Network:  "tcp",
		Addrs:    []string{"localhost:8888"},
		EpollMod: nbio.EPOLLET,
	})

	// g.OnData(onData)
	g.OnRead(func(c *nbio.Conn) {
		buf := make([]byte, 1024)
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		c.Write(buf[:n])
	})
	err := g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	g.Wait()
}
