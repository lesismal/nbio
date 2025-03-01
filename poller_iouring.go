// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux && iouring
// +build linux,iouring

package nbio

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
	"golang.org/x/sys/unix"
)

const (
	// io_uring操作码
	IORING_OP_NOP            = 0
	IORING_OP_READV          = 1
	IORING_OP_WRITEV         = 2
	IORING_OP_FSYNC          = 3
	IORING_OP_READ           = 4
	IORING_OP_WRITE          = 5
	IORING_OP_POLL_ADD       = 6
	IORING_OP_POLL_REMOVE    = 7
	IORING_OP_ACCEPT         = 17
	IORING_OP_CONNECT        = 18
	IORING_OP_TIMEOUT        = 11
	IORING_OP_TIMEOUT_REMOVE = 12

	// io_uring标志
	IORING_ENTER_GETEVENTS = 1
	IORING_SETUP_SQPOLL    = 2
	IORING_SETUP_IOPOLL    = 4

	// io_uring事件标志
	POLL_IN  = 0x01
	POLL_OUT = 0x04
	POLL_ERR = 0x08
	POLL_HUP = 0x10
)

// io_uring_sqe结构体
type ioUringSQE struct {
	opcode      uint8
	flags       uint8
	ioprio      uint16
	fd          int32
	off         uint64
	addr        uint64
	len         uint32
	opFlags     uint32
	userData    uint64
	bufIndex    uint16
	personality uint16
	_pad        [2]uint64
}

// io_uring_cqe结构体
type ioUringCQE struct {
	userData uint64
	res      int32
	flags    uint32
}

// io_uring_params结构体
type ioUringParams struct {
	sqEntries    uint32
	cqEntries    uint32
	flags        uint32
	sqThreadCpu  uint32
	sqThreadIdle uint32
	features     uint32
	wqFd         uint32
	resv         [3]uint32
	sqOff        struct {
		head     uint32
		tail     uint32
		ringMask uint32
		entries  uint32
		flags    uint32
		dropped  uint32
		array    uint32
		resv1    uint32
		resv2    uint64
	}
	cqOff struct {
		head     uint32
		tail     uint32
		ringMask uint32
		entries  uint32
		overflow uint32
		cqes     uint32
		flags    uint32
		resv1    uint32
		resv2    uint64
	}
}

// IOUringPoller 实现了基于io_uring的轮询器
type IOUringPoller struct {
	g *Engine // 父引擎

	ringFd    int          // io_uring文件描述符
	sqRing    []byte       // 提交队列环
	cqRing    []byte       // 完成队列环
	sqEntries []ioUringSQE // 提交队列条目
	sqHead    *uint32      // 提交队列头指针
	sqTail    *uint32      // 提交队列尾指针
	sqMask    *uint32      // 提交队列掩码指针
	sqArray   *uint32      // 提交队列数组指针
	cqHead    *uint32      // 完成队列头指针
	cqTail    *uint32      // 完成队列尾指针
	cqMask    *uint32      // 完成队列掩码指针
	cqEntries []ioUringCQE // 完成队列条目

	index int // 轮询器索引

	pollType string // 轮询器类型

	shutdown bool // 状态

	// 是否用于监听器
	isListener bool
	// 监听器
	listener net.Listener
	// 如果轮询器用作UnixConn监听器，
	// 存储地址并在退出时删除
	unixSockAddr string

	ReadBuffer []byte // 默认读取缓冲区
}

// 将连接添加到轮询器并处理其IO事件
//
//go:norace
func (p *IOUringPoller) addConn(c *Conn) error {
	fd := c.fd
	if fd >= len(p.g.connsUnix) {
		err := fmt.Errorf("too many open files, fd[%d] >= MaxOpenFiles[%d]",
			fd,
			len(p.g.connsUnix),
		)
		c.closeWithError(err)
		return err
	}
	c.p = p
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	} else {
		p.g.onUDPListen(c)
	}
	p.g.connsUnix[fd] = c
	err := p.addRead(fd)
	if err != nil {
		p.g.connsUnix[fd] = nil
		c.closeWithError(err)
	}
	return err
}

// 将拨号器添加到轮询器并处理其IO事件
//
//go:norace
func (p *IOUringPoller) addDialer(c *Conn) error {
	fd := c.fd
	if fd >= len(p.g.connsUnix) {
		err := fmt.Errorf("too many open files, fd[%d] >= MaxOpenFiles[%d]",
			fd,
			len(p.g.connsUnix),
		)
		c.closeWithError(err)
		return err
	}
	c.p = p
	p.g.connsUnix[fd] = c
	c.isWAdded = true
	err := p.addReadWrite(fd)
	if err != nil {
		p.g.connsUnix[fd] = nil
		c.closeWithError(err)
	}
	return err
}

//go:norace
func (p *IOUringPoller) getConn(fd int) *Conn {
	return p.g.connsUnix[fd]
}

//go:norace
func (p *IOUringPoller) deleteConn(c *Conn) {
	if c == nil {
		return
	}
	fd := c.fd

	if c.typ != ConnTypeUDPClientFromRead {
		if c == p.g.connsUnix[fd] {
			p.g.connsUnix[fd] = nil
		}
	}

	if c.typ != ConnTypeUDPServer {
		p.g.onClose(c, c.closeErr)
	}
}

//go:norace
func (p *IOUringPoller) start() {
	defer p.g.Done()

	logging.Debug("NBIO[%v][%v_%v] start", p.g.Name, p.pollType, p.index)
	defer logging.Debug("NBIO[%v][%v_%v] stopped", p.g.Name, p.pollType, p.index)

	if p.isListener {
		p.acceptorLoop()
	} else {
		defer func() {
			unix.Close(p.ringFd)
		}()
		p.readWriteLoop()
	}
}

//go:norace
func (p *IOUringPoller) acceptorLoop() {
	if p.g.LockListener {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	p.shutdown = false
	for !p.shutdown {
		conn, err := p.listener.Accept()
		if err == nil {
			var c *Conn
			c, err = NBConn(conn)
			if err != nil {
				conn.Close()
				continue
			}
			err = p.g.pollers[c.Hash()%len(p.g.pollers)].addConn(c)
			if err != nil {
				logging.Error("NBIO[%v][%v_%v] addConn [fd: %v] failed: %v",
					p.g.Name,
					p.pollType,
					p.index,
					c.fd,
					err,
				)
			}
		} else {
			var ne net.Error
			if ok := errors.As(err, &ne); ok && ne.Timeout() {
				logging.Error("NBIO[%v][%v_%v] Accept failed: timeout error, retrying...",
					p.g.Name,
					p.pollType,
					p.index,
				)
				time.Sleep(time.Second / 20)
			} else {
				if !p.shutdown {
					logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...",
						p.g.Name,
						p.pollType,
						p.index,
						err,
					)
				}
				break
			}
		}
	}
}

//go:norace
func (p *IOUringPoller) readWriteLoop() {
	if p.g.LockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	g := p.g
	p.shutdown = false

	// 设置超时
	var timeoutSpec unix.Timespec
	timeoutSpec.Sec = 0
	timeoutSpec.Nsec = 20 * 1000 * 1000 // 20ms

	// 准备超时SQE
	timeoutAddr := uintptr(unsafe.Pointer(&timeoutSpec))

	for !p.shutdown {
		// 提交超时SQE
		sqeIndex := *p.sqTail & *p.sqMask
		sqe := &p.sqEntries[sqeIndex]
		sqe.opcode = IORING_OP_TIMEOUT
		sqe.fd = -1
		sqe.addr = uint64(timeoutAddr)
		sqe.len = 1
		sqe.userData = 0 // 特殊值表示超时

		// 更新提交队列尾
		*p.sqTail++
		p.sqArray[sqeIndex] = sqeIndex

		// 提交SQE并等待完成
		_, err := unix.Syscall(unix.SYS_IO_URING_ENTER, uintptr(p.ringFd), 1, 1, uintptr(unsafe.Pointer(&timeoutSpec)), 0, 0)
		if err != 0 && err != unix.EINTR && err != unix.EAGAIN {
			logging.Error("NBIO[%v][%v_%v] io_uring_enter failed: %v, exit...",
				p.g.Name,
				p.pollType,
				p.index,
				err,
			)
			return
		}

		// 处理完成事件
		head := *p.cqHead
		for head != *p.cqTail {
			cqe := &p.cqEntries[head&*p.cqMask]

			// 处理非超时事件
			if cqe.userData != 0 {
				fd := int(cqe.userData >> 32)
				eventType := uint32(cqe.userData)

				c := p.getConn(fd)
				if c != nil {
					// 处理读事件
					if eventType&POLL_IN != 0 {
						if g.onRead == nil {
							for i := 0; i < g.MaxConnReadTimesPerEventLoop; i++ {
								buffer := g.borrow(c)
								rc, n, err := c.ReadAndGetConn(buffer)
								if n > 0 {
									g.onData(rc, buffer[:n])
								}
								g.payback(c, buffer)
								if errors.Is(err, syscall.EINTR) {
									continue
								}
								if errors.Is(err, syscall.EAGAIN) {
									break
								}
								if err != nil {
									c.closeWithError(err)
									break
								}
								if n < len(buffer) {
									break
								}
							}

							// 重新添加读事件
							p.addRead(fd)
						} else {
							g.onRead(c)
						}
					}

					// 处理写事件
					if eventType&POLL_OUT != 0 {
						if c.onConnected == nil {
							c.flush()
						} else {
							c.onConnected(c, nil)
							c.onConnected = nil
							c.resetRead()
						}
					}

					// 处理错误事件
					if eventType&(POLL_ERR|POLL_HUP) != 0 {
						c.closeWithError(io.EOF)
					}
				}
			}

			// 更新完成队列头
			head++
			*p.cqHead = head
		}
	}
}

//go:norace
func (p *IOUringPoller) stop() {
	logging.Debug("NBIO[%v][%v_%v] stop...", p.g.Name, p.pollType, p.index)
	p.shutdown = true
	if p.listener != nil {
		p.listener.Close()
		if p.unixSockAddr != "" {
			os.Remove(p.unixSockAddr)
		}
	}
}

//go:norace
func (p *IOUringPoller) addRead(fd int) error {
	// 添加读事件
	sqeIndex := *p.sqTail & *p.sqMask
	sqe := &p.sqEntries[sqeIndex]
	sqe.opcode = IORING_OP_POLL_ADD
	sqe.fd = int32(fd)
	sqe.addr = 0
	sqe.len = 0
	sqe.opFlags = POLL_IN
	sqe.userData = uint64(fd)<<32 | POLL_IN

	// 更新提交队列尾
	*p.sqTail++
	p.sqArray[sqeIndex] = sqeIndex

	// 提交SQE
	_, err := unix.Syscall(unix.SYS_IO_URING_ENTER, uintptr(p.ringFd), 1, 0, 0, 0, 0)
	return err
}

//go:norace
func (p *IOUringPoller) addReadWrite(fd int) error {
	// 添加读写事件
	sqeIndex := *p.sqTail & *p.sqMask
	sqe := &p.sqEntries[sqeIndex]
	sqe.opcode = IORING_OP_POLL_ADD
	sqe.fd = int32(fd)
	sqe.addr = 0
	sqe.len = 0
	sqe.opFlags = POLL_IN | POLL_OUT
	sqe.userData = uint64(fd)<<32 | (POLL_IN | POLL_OUT)

	// 更新提交队列尾
	*p.sqTail++
	p.sqArray[sqeIndex] = sqeIndex

	// 提交SQE
	_, err := unix.Syscall(unix.SYS_IO_URING_ENTER, uintptr(p.ringFd), 1, 0, 0, 0, 0)
	return err
}

//go:norace
func (p *IOUringPoller) modWrite(fd int) error {
	// 移除旧的事件
	sqeIndex := *p.sqTail & *p.sqMask
	sqe := &p.sqEntries[sqeIndex]
	sqe.opcode = IORING_OP_POLL_REMOVE
	sqe.fd = int32(fd)
	sqe.userData = uint64(fd) << 32

	// 更新提交队列尾
	*p.sqTail++
	p.sqArray[sqeIndex] = sqeIndex

	// 提交SQE
	_, err := unix.Syscall(unix.SYS_IO_URING_ENTER, uintptr(p.ringFd), 1, 0, 0, 0, 0)
	if err != 0 && err != unix.EINTR && err != unix.EAGAIN {
		return err
	}

	// 添加新的读写事件
	return p.addReadWrite(fd)
}

//go:norace
func newIOUringPoller(g *Engine, isListener bool, index int) (*IOUringPoller, error) {
	if isListener {
		if len(g.Addrs) == 0 {
			panic("invalid listener num")
		}

		addr := g.Addrs[index%len(g.Addrs)]
		ln, err := g.Listen(g.Network, addr)
		if err != nil {
			return nil, err
		}

		p := &IOUringPoller{
			g:          g,
			index:      index,
			listener:   ln,
			isListener: isListener,
			pollType:   "LISTENER",
		}
		if g.Network == "unix" {
			p.unixSockAddr = addr
		}

		return p, nil
	}

	// 创建io_uring实例
	var params ioUringParams
	ringFd, err := unix.Syscall(unix.SYS_IO_URING_SETUP, 128, uintptr(unsafe.Pointer(&params)), 0)
	if err != 0 {
		return nil, err
	}

	// 映射提交队列环
	sqRingSize := params.sqOff.array + params.sqEntries*4
	sqRing, err := unix.Mmap(int(ringFd), unix.IORING_OFF_SQ_RING, int(sqRingSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		unix.Close(int(ringFd))
		return nil, err
	}

	// 映射完成队列环
	cqRingSize := params.cqOff.cqes + params.cqEntries*uint32(unsafe.Sizeof(ioUringCQE{}))
	cqRing, err := unix.Mmap(int(ringFd), unix.IORING_OFF_CQ_RING, int(cqRingSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		unix.Munmap(sqRing)
		unix.Close(int(ringFd))
		return nil, err
	}

	// 映射提交队列条目
	sqEntriesSize := params.sqEntries * uint32(unsafe.Sizeof(ioUringSQE{}))
	sqEntriesBytes, err := unix.Mmap(int(ringFd), unix.IORING_OFF_SQES, int(sqEntriesSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		unix.Munmap(cqRing)
		unix.Munmap(sqRing)
		unix.Close(int(ringFd))
		return nil, err
	}

	// 获取提交队列指针
	sqHead := (*uint32)(unsafe.Pointer(&sqRing[params.sqOff.head]))
	sqTail := (*uint32)(unsafe.Pointer(&sqRing[params.sqOff.tail]))
	sqMask := (*uint32)(unsafe.Pointer(&sqRing[params.sqOff.ringMask]))
	sqArray := (*uint32)(unsafe.Pointer(&sqRing[params.sqOff.array]))

	// 获取完成队列指针
	cqHead := (*uint32)(unsafe.Pointer(&cqRing[params.cqOff.head]))
	cqTail := (*uint32)(unsafe.Pointer(&cqRing[params.cqOff.tail]))
	cqMask := (*uint32)(unsafe.Pointer(&cqRing[params.cqOff.ringMask]))
	cqEntries := unsafe.Slice((*ioUringCQE)(unsafe.Pointer(&cqRing[params.cqOff.cqes])), params.cqEntries)

	// 获取提交队列条目
	sqEntries := unsafe.Slice((*ioUringSQE)(unsafe.Pointer(&sqEntriesBytes[0])), params.sqEntries)

	p := &IOUringPoller{
		g:          g,
		ringFd:     int(ringFd),
		sqRing:     sqRing,
		cqRing:     cqRing,
		sqEntries:  sqEntries,
		sqHead:     sqHead,
		sqTail:     sqTail,
		sqMask:     sqMask,
		sqArray:    sqArray,
		cqHead:     cqHead,
		cqTail:     cqTail,
		cqMask:     cqMask,
		cqEntries:  cqEntries,
		index:      index,
		isListener: isListener,
		pollType:   "POLLER",
	}

	return p, nil
}

//go:norace
func (c *Conn) ResetPollerEvent() {
	p := c.p
	g := p.g
	fd := c.fd
	if g.isOneshot && !c.closed {
		if len(c.writeList) == 0 {
			p.addRead(fd)
		} else {
			p.modWrite(fd)
		}
	}
}
