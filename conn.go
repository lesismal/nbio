// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"net"
	"time"
)

// OnData registers callback for data
func (c *Conn) OnData(h func(conn *Conn, data []byte)) {
	c.DataHandler = h
}

// Dial wraps net.Dial
func Dial(network string, address string) (*Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return NBConn(conn)
}

// DialTimeout wraps net.DialTimeout
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

func (c *Conn) ExecuteLen() int {
	c.mux.Lock()
	n := len(c.execList)
	c.mux.Unlock()
	return n
}

func (c *Conn) Execute(f func()) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return
	}

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
