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

func (ps *ProtocolStack) Add(p Protocol) {
	ps.stack = append(ps.stack, p)
}

func (ps *ProtocolStack) Delete(p Protocol) {
	for i, v := range ps.stack {
		if v == p {
			ps.stack[i] = nil
			return
		}
	}
}

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

func (ps *ProtocolStack) Write(b []byte) (int, error) {
	return -1, ErrUnsupported
}
