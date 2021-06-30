package websocket

type LargeMessageWriter struct {
	conn       *Conn
	firstFrame bool
	opcode     MessageType
}

func NewLargeMessageWriter(conn *Conn, opcode MessageType) *LargeMessageWriter {
	return &LargeMessageWriter{
		conn:       conn,
		firstFrame: true,
		opcode:     opcode,
	}
}

func (l *LargeMessageWriter) WriteFin(data []byte, fin bool) (int, error) {
	total := 0
	if len(data) == 0 {
		return 0, l.conn.writeMessage(l.opcode, l.firstFrame, fin && len(data) == 0, []byte{})
	}
	for len(data) > 0 {
		n := len(data)
		if n > framePayloadSize {
			n = framePayloadSize
		}
		if err := l.conn.writeMessage(l.opcode, l.firstFrame, fin && len(data) == n, data[:n]); err != nil {
			return total, err
		}
		l.firstFrame = false
		total += n
		data = data[n:]
	}
	return total, nil
}

func (l *LargeMessageWriter) Write(data []byte) (int, error) {
	return l.WriteFin(data, false)
}
func (l *LargeMessageWriter) Close() error {
	return l.conn.writeMessage(l.opcode, l.firstFrame, true, []byte{})
}
