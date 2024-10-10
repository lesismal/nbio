package nbio

import (
	"net"
)

type Protocol interface {
	Parse(c net.Conn, b []byte, ps *ProtocolStack) (net.Conn, []byte, error)
	Write(b []byte) (int, error)
}

type ProtocolStack struct {
	stack []Protocol
}

//go:norace
func (ps *ProtocolStack) Add(p Protocol) {
	ps.stack = append(ps.stack, p)
}

//go:norace
func (ps *ProtocolStack) Delete(p Protocol) {
	i := len(ps.stack) - 1
	for i >= 0 {
		if ps.stack[i] == p {
			ps.stack[i] = nil
			if i+1 > len(ps.stack)-1 {
				ps.stack = ps.stack[:i]
			} else {
				ps.stack = append(ps.stack[:i], ps.stack[i+1:]...)
			}
			return
		}
		i--
	}
}

//go:norace
func (ps *ProtocolStack) Parse(c net.Conn, b []byte, ps_ ProtocolStack) (net.Conn, []byte, error) {
	var err error
	for _, p := range ps.stack {
		if p == nil {
			continue
		}
		c, b, err = p.Parse(c, b, ps)
		if err != nil {
			break
		}
	}
	return c, b, err
}

//go:norace
func (ps *ProtocolStack) Write(b []byte) (int, error) {
	return -1, ErrUnsupported
}
