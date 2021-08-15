// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"net"
	"time"
)

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
					c.mux.Unlock()
					c.execList = c.execList[0:0]
					return
				}
				f = c.execList[i]
				c.mux.Unlock()
			}
		})
	}
}
