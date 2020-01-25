package nbio

const (
	_EVENT_OPEN = iota
	_EVENT_CLOSE
	_EVENT_DATA
)

type event struct {
	c *Conn  // conn
	t int    // event type
	b []byte // buffer
}
