// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
)

// Response represents the server side of an HTTP response.
type Response struct {
	parser *Parser

	request *http.Request // request for this response

	status     string
	statusCode int // status code passed to WriteHeader

	header      http.Header
	trailer     map[string]string
	trailerSize int

	buffer       []byte
	bodyBuffer   []byte
	intFormatBuf [10]byte

	chunked        bool
	chunkChecked   bool
	headEncoded    bool
	hasBody        bool
	enableSendfile bool
	hijacked       bool
}

// Hijack .
func (res *Response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if res.parser.Processor == nil {
		return nil, nil, errors.New("nil Proccessor")
	}
	res.hijacked = true
	return res.parser.Processor.Conn(), nil, nil
}

// Header .
func (res *Response) Header() http.Header {
	return res.header
}

// WriteHeader .
func (res *Response) WriteHeader(statusCode int) {
	if !res.hijacked && res.statusCode == 0 && res.statusCode != statusCode {
		status := http.StatusText(statusCode)
		if status != "" {
			res.status = status
			res.statusCode = statusCode
		}

		if cl := res.header.Get(contentLengthHeader); cl != "" {
			v, err := strconv.ParseInt(cl, 10, 64)
			if err == nil && v >= 0 {
			} else {
				logging.Error("http: invalid Content-Length of %q", cl)
				res.header.Del(contentLengthHeader)
			}
		}

		res.checkChunked()
	}
}

const maxPacketSize = 65536

// WriteString .
func (res *Response) WriteString(s string) (int, error) {
	x := (*[2]uintptr)(unsafe.Pointer(&s))
	h := [3]uintptr{x[0], x[1], x[1]}
	data := *(*[]byte)(unsafe.Pointer(&h))
	return res.Write(data)
}

// Write .
func (res *Response) Write(data []byte) (int, error) {
	l := len(data)
	conn := res.parser.Processor.Conn()
	if l == 0 || conn == nil {
		return 0, nil
	}

	res.WriteHeader(http.StatusOK)

	res.hasBody = true

	if res.chunked {
		res.eoncodeHead()

		buf := res.buffer
		hl := len(buf)
		res.buffer = nil
		lenStr := res.formatInt(l, 16)
		size := hl + len(lenStr) + l + 4
		if size < maxPacketSize {
			buf = mempool.AppendString(buf, lenStr)
			buf = mempool.AppendString(buf, "\r\n")
			buf = mempool.Append(buf, data...)
			buf = mempool.AppendString(buf, "\r\n")
			res.buffer = buf
			return l, nil
		}
		buf = mempool.AppendString(buf, lenStr)
		buf = mempool.AppendString(buf, "\r\n")
		_, err := conn.Write(buf)
		mempool.Free(buf)
		if err != nil {
			return 0, err
		}
		buf = mempool.Malloc(len(data)+2)[0:0]
		buf = mempool.Append(buf, data...)
		buf = mempool.AppendString(buf, "\r\n")
		if len(buf) < maxPacketSize {
			res.buffer = buf
			return l, nil
		}
		nw, err := conn.Write(buf)
		mempool.Free(buf)
		if err == nil {
			return l, nil
		}
		if nw >= 4 {
			return nw - 4, err
		}
		return -1, err
	}

	if len(res.header[contentLengthHeader]) > 0 {
		res.eoncodeHead()

		buf := res.buffer
		res.buffer = nil
		if buf == nil {
			return conn.Write(buf)
		}
		buf = mempool.Append(buf, data...)
		nw, err := conn.Write(buf)
		mempool.Free(buf)
		return nw, err
	}
	if res.bodyBuffer == nil {
		res.bodyBuffer = mempool.Malloc(l)[0:0]
	}

	res.bodyBuffer = mempool.Append(res.bodyBuffer, data...)
	// res.header[contentLengthHeader] = []string{res.formatInt(l, 10)}
	return l, nil
}

// ReadFrom .
func (res *Response) ReadFrom(r io.Reader) (n int64, err error) {
	c := res.parser.Processor.Conn()
	if c == nil {
		return 0, nil
	}

	res.hasBody = true
	res.eoncodeHead()
	_, err = c.Write(res.buffer)
	res.buffer = nil
	if err != nil {
		return 0, err
	}

	if res.enableSendfile {
		lr, ok := r.(*io.LimitedReader)
		if ok {
			n, r = lr.N, lr.R
			if n <= 0 {
				return 0, nil
			}
		}

		f, ok := r.(*os.File)
		if ok {
			nc, ok := c.(interface {
				Sendfile(f *os.File, remain int64) (int64, error)
			})
			if ok {
				ns, err := nc.Sendfile(f, lr.N)
				return ns, err
			}
		}
	}

	return io.Copy(c, r)
}

// checkChunked .
func (res *Response) checkChunked() {
	if res.chunkChecked {
		return
	}

	res.chunkChecked = true

	// res.WriteHeader(http.StatusOK)

	if res.request.ProtoAtLeast(1, 1) {
		for _, v := range res.header[transferEncodingHeader] {
			if v == "chunked" {
				res.chunked = true
			}
		}
		if !res.chunked {
			if len(res.header[trailerHeader]) > 0 {
				res.chunked = true
				hs := res.header[transferEncodingHeader]
				res.header[transferEncodingHeader] = append(hs, "chunked")
			}
		}
	}
	if res.chunked {
		delete(res.header, contentLengthHeader)
	}
}

// flush .
func (res *Response) eoncodeHead() {
	if res.headEncoded {
		return
	}

	res.WriteHeader(http.StatusOK)

	res.headEncoded = true

	status := res.status
	statusCode := res.statusCode

	data := mempool.Malloc(1024)[0:0]

	data = mempool.AppendString(data, res.request.Proto)
	data = mempool.Append(data, ' ', '0'+byte(statusCode/100), '0'+byte(statusCode%100)/10, '0'+byte(statusCode%10), ' ')
	data = mempool.AppendString(data, status)
	data = mempool.Append(data, '\r', '\n')

	if res.hasBody && len(res.header["Content-Type"]) == 0 {
		const contentType = "Content-Type: text/plain; charset=utf-8\r\n"
		data = mempool.AppendString(data, contentType)
	}
	if !res.chunked {
		const contentLenthKey = "Content-Length: "
		if !res.hasBody {
			data = mempool.AppendString(data, contentLenthKey)
			data = mempool.Append(data, '0', '\r', '\n')
		} else {
			data = mempool.AppendString(data, contentLenthKey)
			l := len(res.bodyBuffer)
			if l > 0 {
				s := strconv.FormatInt(int64(l), 10)
				data = mempool.AppendString(data, s)
				data = mempool.Append(data, '\r', '\n')
			} else {
				data = mempool.Append(data, '0', '\r', '\n')
			}
		}
	}
	if res.request.Close && len(res.header["Connection"]) == 0 {
		const connection = "Connection: close\r\n"
		data = mempool.AppendString(data, connection)
	}

	if len(res.header["Date"]) == 0 {
		const days = "SunMonTueWedThuFriSat"
		const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
		t := time.Now().UTC()
		yy, mm, dd := t.Date()
		hh, mn, ss := t.Clock()
		day := days[3*t.Weekday():]
		mon := months[3*(mm-1):]
		data = mempool.Append(data,
			'D', 'a', 't', 'e', ':', ' ',
			day[0], day[1], day[2], ',', ' ',
			byte('0'+dd/10), byte('0'+dd%10), ' ',
			mon[0], mon[1], mon[2], ' ',
			byte('0'+yy/1000), byte('0'+(yy/100)%10), byte('0'+(yy/10)%10), byte('0'+yy%10), ' ',
			byte('0'+hh/10), byte('0'+hh%10), ':',
			byte('0'+mn/10), byte('0'+mn%10), ':',
			byte('0'+ss/10), byte('0'+ss%10), ' ',
			'G', 'M', 'T',
			'\r', '\n')
	}

	res.trailer = map[string]string{}
	trailers := res.header[trailerHeader]
	for _, k := range trailers {
		res.trailer[k] = ""
	}
	for k, vv := range res.header {
		if _, ok := res.trailer[k]; !ok {
			for _, v := range vv {
				data = mempool.AppendString(data, k)
				data = mempool.Append(data, ':', ' ')
				data = mempool.AppendString(data, v)
				data = mempool.Append(data, '\r', '\n')
			}
		} else if len(vv) > 0 {
			v := res.header.Get(k)
			res.trailer[k] = v
			res.trailerSize += (len(k) + len(v) + 4)
		}
	}

	data = mempool.Append(data, '\r', '\n')
	res.buffer = data
}

func (res *Response) flushTrailer(conn io.Writer) error {
	var err error

	if !res.chunked {
		if res.buffer != nil {
			if res.bodyBuffer != nil {
				res.buffer = mempool.Append(res.buffer, res.bodyBuffer...)
				mempool.Free(res.bodyBuffer)
				res.bodyBuffer = nil
			}
			_, err = conn.Write(res.buffer)
			mempool.Free(res.buffer)
			res.buffer = nil
			if err != nil {
				return err
			}
		}
		if res.bodyBuffer != nil {
			_, err = conn.Write(res.bodyBuffer)
			mempool.Free(res.bodyBuffer)
			res.bodyBuffer = nil
		}

		return err
	}

	data := res.buffer
	res.buffer = nil
	if data == nil {
		data = mempool.Malloc(0)
	}
	if len(res.trailer) == 0 {
		data = mempool.AppendString(data, "0\r\n\r\n")
	} else {
		data = mempool.AppendString(data, "0\r\n")
		for k, v := range res.trailer {
			data = mempool.AppendString(data, k)
			data = mempool.AppendString(data, ": ")
			data = mempool.AppendString(data, v)
			data = mempool.AppendString(data, "\r\n")
		}
		data = mempool.AppendString(data, "\r\n")
	}
	_, err = conn.Write(data)
	mempool.Free(data)
	return err
}

var numMap = []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

func (res *Response) formatInt(n int, base int) string {
	if n < 0 || n > 0x7FFFFFFF {
		return ""
	}

	buf := res.intFormatBuf[:]
	i := len(buf)
	for {
		i--
		buf[i] = numMap[n%base]
		n /= base
		if n <= 0 {
			break
		}
	}
	return string(buf[i:])
}

// NewResponse .
func NewResponse(parser *Parser, request *http.Request, enableSendfile bool) *Response {
	res := responsePool.Get().(*Response)
	res.parser = parser
	res.request = request
	res.header = http.Header{ /*"Server": []string{"nbio"}*/ }
	res.enableSendfile = enableSendfile
	return res
}
