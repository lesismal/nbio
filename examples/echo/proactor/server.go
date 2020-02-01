package main

import (
	"fmt"
	"github.com/lesismal/nbio"
	"syscall"
	"time"
)

type Buffer struct {
	c    *nbio.Conn
	d    []byte
	e    error
	done chan struct{}
}

var chBuffers = make(chan *Buffer, 128)

func onRead(c *nbio.Conn, b []byte) (int, error) {
	buffer := <-chBuffers
	n, err := syscall.Read(int(c.Fd()), buffer.d)
	if n > 0 {
		buffer.d = buffer.d[:n]
	}
	buffer.c = c
	buffer.e = err
	buffer.done <- struct{}{}
	return n, err
}

func Read(data []byte) (*nbio.Conn, int, error) {
	buffer := &Buffer{d: data, done: make(chan struct{}, 1)}
	chBuffers <- buffer
	<-buffer.done
	return buffer.c, len(buffer.d), buffer.e
}

func main() {
	g, err := nbio.NewGopher(nbio.Config{
		Network: "tcp",
		Address: ":8888",
	})
	if err != nil {
		fmt.Printf("nbio.New failed: %v\n", err)
		return
	}

	g.OnOpen(func(c *nbio.Conn) {
		c.SetReadDeadline(time.Now().Add(time.Second * 10))
	})
	g.OnRead(onRead)

	err = g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	for {
		buffer := make([]byte, 1024)
		c, n, err := Read(buffer)
		if c != nil && n > 0 && err == nil {
			c.SetReadDeadline(time.Now().Add(time.Second * 10))
			c.SetWriteDeadline(time.Now().Add(time.Second * 3))
			c.Write(buffer[:n])
		}
	}

	g.Wait()
}
