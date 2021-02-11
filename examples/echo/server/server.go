package main

import (
	"fmt"
	"time"

	"github.com/lesismal/nbio"
)

func onOpen(c *nbio.Conn) {
	// c.SetReadDeadline(time.Now().Add(time.Second * 3))
	fmt.Println("onOpen:", c.RemoteAddr().String(), time.Now().Format("15:04:05.000"))
}

func onClose(c *nbio.Conn, err error) {
	fmt.Println("onClose:", c.RemoteAddr().String(), time.Now().Format("15:04:05.000"), err)
}

func onData(c *nbio.Conn, data []byte) {
	// c.SetReadDeadline(time.Now().Add(time.Second * 3))
	// c.SetWriteDeadline(time.Now().Add(time.Second * 3))
	c.Write(append([]byte{}, data...))
}

func main() {
	g := nbio.NewGopher(nbio.Config{
		NPoller: 1,
		Network: "tcp",
		Addrs:   []string{"localhost:8888", "localhost:9999"},
	})

	g.OnOpen(onOpen)
	g.OnClose(onClose)
	g.OnData(onData)

	err := g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	/*go func() {
		for {
			time.Sleep(time.Second * 5)
			fmt.Println(g.State().String())
		}
	}()*/

	g.Wait()
}
