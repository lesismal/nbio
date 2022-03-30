// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"net"
	"runtime"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

// OnData registers callback for data.
func (c *Conn) OnData(h func(conn *Conn, data []byte)) {
	c.DataHandler = h
}

// Dial wraps net.Dial.
func Dial(network string, address string) (*Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return NBConn(conn)
}

// DialTimeout wraps net.DialTimeout.
func DialTimeout(network string, address string, timeout time.Duration) (*Conn, error) {
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return nil, err
	}
	return NBConn(conn)
}

// Lock .
func (c *Conn) Lock() {
	c.mux.Lock()
}

// Unlock .
func (c *Conn) Unlock() {
	c.mux.Unlock()
}

// IsClosed .
func (c *Conn) IsClosed() (bool, error) {
	return c.closed, c.closeErr
}

// ExecuteLen .
func (c *Conn) ExecuteLen() int {
	c.mux.Lock()
	n := len(c.execList)
	c.mux.Unlock()
	return n
}

// Execute .
func (c *Conn) Execute(f func()) bool {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return false
	}

	isHead := (len(c.execList) == 0)
	c.execList = append(c.execList, f)
	c.mux.Unlock()

	if isHead {
		c.g.Execute(func() {
			i := 0
			for {
				func() {
					defer func() {
						if err := recover(); err != nil {
							const size = 64 << 10
							buf := make([]byte, size)
							buf = buf[:runtime.Stack(buf, false)]
							logging.Error("conn execute failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
						}
					}()
					f()
				}()

				c.mux.Lock()
				i++
				if len(c.execList) == i {
					c.execList = c.execList[0:0]
					c.mux.Unlock()
					return
				}
				f = c.execList[i]
				c.mux.Unlock()
			}
		})
	}

	return true
}

// MustExecute .
func (c *Conn) MustExecute(f func()) {
	c.mux.Lock()
	isHead := (len(c.execList) == 0)
	c.execList = append(c.execList, f)
	c.mux.Unlock()

	if isHead {
		c.g.Execute(func() {
			i := 0
			for {
				f()

				c.mux.Lock()
				i++
				if len(c.execList) == i {
					c.execList = c.execList[0:0]
					c.mux.Unlock()
					return
				}
				f = c.execList[i]
				c.mux.Unlock()
			}
		})
	}
}
