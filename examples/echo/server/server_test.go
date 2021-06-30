package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/lesismal/nbio"
)

const (
	addr = ":9101"
)

var (
	buf = make([]byte, 6*1024*1024)
)

func init() {
	rand.Read(buf)
}

func writeComplete(c *nbio.Conn, data []byte) (int, error) {
	offset := 0
	msgLen := len(data)
	for {
		n, err := c.Write(data[offset:])
		offset += n
		if err != nil || offset == msgLen {
			return offset, err
		} else {
			time.Sleep(time.Millisecond * 500)
		}
	}

}

func server(ready chan struct{}) error {
	g := nbio.NewGopher(nbio.Config{
		Network:            "tcp",
		Addrs:              []string{addr},
		MaxWriteBufferSize: 6 * 1024 * 1024,
	})

	g.OnOpen(func(c *nbio.Conn) {
		_, err := writeComplete(c, buf)
		if err != nil {
			fmt.Printf("write failed: %s\n", err)
		}
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		g.Stop()
	})

	err := g.Start()
	if err != nil {
		return fmt.Errorf("nbio.Start failed: %w\n", err)
	}
	ready <- struct{}{}
	defer g.Stop()

	g.Wait()
	return nil
}
func client(msgLen int) error {
	var (
		ret  []byte
		addr = addr
	)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Println(err)
		return err
	}

	i := 0
	line := make([]byte, 60000)
	for {

		n, err := c.Read(line)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("error read: %d %w", n, err)
		}
		if errors.Is(err, io.EOF) {
			time.Sleep(time.Second * 5)
		}
		i++
		ret = append(ret, line[:n]...)
		fmt.Printf("client received %d %d %d of %d\n", i, n, len(ret), len(buf))
		if len(ret) == len(buf) {
			if bytes.Equal(buf, ret) {
				return nil
			} else {
				return fmt.Errorf("ret, does not match buf")
			}
		}

	}
}
func Test_main(t *testing.T) {
	ready := make(chan struct{})
	go func() {
		err := server(ready)
		if err != nil {
			t.Fatal(err)
		}
	}()
	<-ready

	err := client(1024 * 1024 * 4)
	if err != nil {
		t.Fatal(err)
	}
}
