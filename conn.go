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

// ConnType is used to identify different types of Conn.
type ConnType = int8

const (
	// ConnTypeTCP represents TCP Conn.
	ConnTypeTCP ConnType = iota + 1
	// ConnTypeUDPServer represents UDP Conn used as a listener.
	ConnTypeUDPServer
	// ConnTypeUDPClientFromRead represents UDP connection that
	// is sending data to our UDP Server from peer.
	ConnTypeUDPClientFromRead
	// ConnTypeUDPClientFromDial represents UDP Conn that is sending
	// data to other UDP Server from ourselves.
	ConnTypeUDPClientFromDial
	// ConnTypeUnix represents Unix Conn.
	ConnTypeUnix
)

// Type .
//
//go:norace
func (c *Conn) Type() ConnType {
	return c.typ
}

// IsTCP returns whether this Conn is a TCP Conn.
//
//go:norace
func (c *Conn) IsTCP() bool {
	return c.typ == ConnTypeTCP
}

// IsUDP returns whether this Conn is a UDP Conn.
//
//go:norace
func (c *Conn) IsUDP() bool {
	switch c.typ {
	case ConnTypeUDPServer, ConnTypeUDPClientFromDial, ConnTypeUDPClientFromRead:
		return true
	}
	return false
}

// IsUnix  returns whether this Conn is a Unix Conn.
//
//go:norace
func (c *Conn) IsUnix() bool {
	return c.typ == ConnTypeUnix
}

// Session returns user session.
//
//go:norace
func (c *Conn) Session() interface{} {
	return c.session
}

// SetSession sets user session.
//
//go:norace
func (c *Conn) SetSession(session interface{}) {
	c.session = session
}

// OnData registers Conn's data handler.
// Notice:
//  1. The data readed by the poller is not handled by this Conn's data
//     handler by default.
//  2. The data readed by the poller is handled by nbio.Engine's data
//     handler which is registered by nbio.Engine.OnData by default.
//  3. This Conn's data handler is used to customize your implementation,
//     you can set different data handler for different Conns,
//     and call Conn's data handler in nbio.Engine's data handler.
//     For example:
//     engine.OnData(func(c *nbio.Conn, data byte){
//     c.DataHandler()(c, data)
//     })
//     conn1.OnData(yourDatahandler1)
//     conn2.OnData(yourDatahandler2)
//
//go:norace
func (c *Conn) OnData(h func(conn *Conn, data []byte)) {
	c.dataHandler = h
}

// DataHandler returns Conn's data handler.
//
//go:norace
func (c *Conn) DataHandler() func(conn *Conn, data []byte) {
	return c.dataHandler
}

// Dial calls net.Dial to make a net.Conn and convert it to *nbio.Conn.
//
//go:norace
func Dial(network string, address string) (*Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return NBConn(conn)
}

// Dial calls net.DialTimeout to make a net.Conn and convert it to *nbio.Conn.
//
//go:norace
func DialTimeout(network string, address string, timeout time.Duration) (*Conn, error) {
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return nil, err
	}
	return NBConn(conn)
}

// Lock .
//
//go:norace
func (c *Conn) Lock() {
	c.mux.Lock()
}

// Unlock .
//
//go:norace
func (c *Conn) Unlock() {
	c.mux.Unlock()
}

// IsClosed returns whether the Conn is closed.
//
//go:norace
func (c *Conn) IsClosed() (bool, error) {
	return c.closed, c.closeErr
}

// ExecuteLen returns the length of the Conn's job list.
//
//go:norace
func (c *Conn) ExecuteLen() int {
	c.mux.Lock()
	n := len(c.jobList)
	c.mux.Unlock()
	return n
}

// Execute is used to run the job.
//
// How it works:
// If the job is the head/first of the Conn's job list, it will call the
// nbio.Engine.Execute to run all the jobs in the job list that include:
//  1. This job
//  2. New jobs that are pushed to the back of the list before this job
//     is done.
//  3. nbio.Engine.Execute returns until there's no more jobs in the job
//     list.
//
// Else if the job is not the head/first of the job list, it will push the
// job to the back of the job list and wait to be called.
// This guarantees there's at most one flow or goroutine running job/jobs
// for each Conn.
// This guarantees all the jobs are executed in order.
//
// Notice:
//  1. The job wouldn't run or pushed to the back of the job list if the
//     connection is closed.
//  2. nbio.Engine.Execute is handled by a goroutine pool by default, users
//     can customize it.
//
//go:norace
func (c *Conn) Execute(job func()) bool {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return false
	}

	isHead := (len(c.jobList) == 0)
	c.jobList = append(c.jobList, job)
	c.mux.Unlock()

	// If there's no job running, run Engine.Execute to run this job
	// and new jobs appended before this head job is done.
	if isHead {
		c.execute(job)
	}
	return true
}

// MustExecute implements a similar function as Execute did,
// but will still execute or push the job to the
// back of the job list no matter whether Conn has been closed,
// it guarantees the job to be executed.
// This is used to handle the close event in nbio/nbhttp.
//
//go:norace
func (c *Conn) MustExecute(job func()) {
	c.mux.Lock()
	isHead := (len(c.jobList) == 0)
	c.jobList = append(c.jobList, job)
	c.mux.Unlock()

	// If there's no job running, run Engine.Execute to run this job
	// and new jobs appended before this head job is done.
	if isHead {
		c.execute(job)
	}
}

//go:norace
func (c *Conn) execute(job func()) {
	c.p.g.Execute(func() {
		i := 0
		for {
			func() {
				defer func() {
					if err := recover(); err != nil {
						const size = 64 << 10
						buf := make([]byte, size)
						buf = buf[:runtime.Stack(buf, false)]
						logging.Error("conn execute failed: %v\n%v\n",
							err,
							*(*string)(unsafe.Pointer(&buf)),
						)
					}
				}()
				job()
			}()

			c.mux.Lock()
			i++
			if len(c.jobList) == i {
				// set nil to release the job and gc
				c.jobList[i-1] = nil
				// reuse the slice
				c.jobList = c.jobList[0:0]
				c.mux.Unlock()
				return
			}
			// get next job
			job = c.jobList[i]
			// set nil to release the job and gc
			c.jobList[i] = nil
			c.mux.Unlock()
		}
	})
}
