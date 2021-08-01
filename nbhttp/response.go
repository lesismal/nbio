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

		if cl := res.header.Get("Content-Length"); cl != "" {
			v, err := strconv.ParseInt(cl, 10, 64)
			if err == nil && v >= 0 {
			} else {
				logging.Error("http: invalid Content-Length of %q", cl)
				res.header.Del("Content-Length")
			}
		}

		res.checkChunked()
	}
}

const maxPacketSize = 65536

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
			buf = append(buf, lenStr...)
			buf = append(buf, "\r\n"...)
			buf = append(buf, data...)
			buf = append(buf, "\r\n"...)
			res.buffer = buf
			return l, nil
		}
		_, err := conn.Write(buf)
		if err != nil {
			return 0, err
		}
		buf = mempool.Malloc(0)
		buf = append(buf, lenStr...)
		buf = append(buf, "\r\n"...)
		buf = append(buf, data...)
		buf = append(buf, "\r\n"...)
		if len(buf) < maxPacketSize {
			res.buffer = buf
			return l, nil
		}
		return conn.Write(buf)
	}

	if len(res.header["Content-Length"]) > 0 {
		res.eoncodeHead()

		buf := res.buffer
		res.buffer = nil
		if buf == nil {
			buf = mempool.Malloc(l)[0:0]
		}
		buf = append(buf, data...)
		return conn.Write(buf)
	}
	if res.bodyBuffer == nil {
		res.bodyBuffer = mempool.Malloc(l)[0:0]
	}

	res.bodyBuffer = append(res.bodyBuffer, data...)
	// res.header["Content-Length"] = []string{res.formatInt(l, 10)}
	return l, nil
}

func (res *Response) ReadFrom(r io.Reader) (n int64, err error) {
	c := res.parser.Processor.Conn()
	if c == nil {
		return 0, nil
	}

	res.hasBody = true
	res.eoncodeHead()
	_, err = c.Write(res.buffer)
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
				return int64(ns), err
			}
		}
	}

	return io.Copy(c, r)
}

// checkChunked .
func (res *Response) checkChunked() error {
	if res.chunkChecked {
		return nil
	}

	res.chunkChecked = true

	// res.WriteHeader(http.StatusOK)

	if res.request.ProtoAtLeast(1, 1) {
		for _, v := range res.header["Transfer-Encoding"] {
			if v == "chunked" {
				res.chunked = true
			}
		}
		if !res.chunked {
			if len(res.header["Trailer"]) > 0 {
				res.chunked = true
				hs := res.header["Transfer-Encoding"]
				res.header["Transfer-Encoding"] = append(hs, "chunked")
			}
		}
	}
	if res.chunked {
		delete(res.header, "Content-Length")
	}

	return nil
}

// flush .
func (res *Response) eoncodeHead() {
	if res.headEncoded {
		return
	}

	res.headEncoded = true

	status := res.status
	statusCode := res.statusCode

	data := mempool.Malloc(1024)[0:0]

	data = append(data, res.request.Proto...)
	data = append(data, ' ', '0'+byte(statusCode/100), '0'+byte(statusCode%100)/10, '0'+byte(statusCode%10), ' ')
	data = append(data, status...)
	data = append(data, '\r', '\n')

	if res.hasBody && len(res.header["Content-Type"]) == 0 {
		const contentType = "Content-Type: text/plain; charset=utf-8\r\n"
		data = append(data, contentType...)
	}
	if !res.chunked {
		const contentLenthKey = "Content-Length: "
		if !res.hasBody {
			data = append(data, contentLenthKey...)
			data = append(data, '0', '\r', '\n')
		} else {
			data = append(data, contentLenthKey...)
			l := len(res.bodyBuffer)
			if l > 0 {
				s := strconv.FormatInt(int64(l), 10)
				data = append(data, s...)
				data = append(data, '\r', '\n')
			} else {
				data = append(data, '0', '\r', '\n')
			}
		}
	}
	if res.request.Close && len(res.header["Connection"]) == 0 {
		const connection = "Connection: close\r\n"
		data = append(data, connection...)
	}

	if len(res.header["Date"]) == 0 {
		const days = "SunMonTueWedThuFriSat"
		const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
		t := time.Now().UTC()
		yy, mm, dd := t.Date()
		hh, mn, ss := t.Clock()
		day := days[3*t.Weekday():]
		mon := months[3*(mm-1):]
		data = append(data,
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
	trailers := res.header["Trailer"]
	for _, k := range trailers {
		res.trailer[k] = ""
	}
	for k, vv := range res.header {
		if _, ok := res.trailer[k]; !ok {
			for _, v := range vv {
				data = append(data, k...)
				data = append(data, ':', ' ')
				data = append(data, v...)
				data = append(data, '\r', '\n')
			}
		} else if len(vv) > 0 {
			v := res.header.Get(k)
			res.trailer[k] = v
			res.trailerSize += (len(k) + len(v) + 4)
		}
	}

	data = append(data, '\r', '\n')
	res.buffer = data
}

func (res *Response) flushTrailer(conn net.Conn) error {
	var err error

	if !res.chunked {
		if res.buffer != nil {
			_, err = conn.Write(res.buffer)
			res.buffer = nil
			if err != nil {
				if res.bodyBuffer != nil {
					mempool.Free(res.bodyBuffer)
				}
				return err
			}
		}
		if res.bodyBuffer != nil {
			_, err = conn.Write(res.bodyBuffer)
			res.bodyBuffer = nil
		}

		return err
	}

	data := res.buffer
	res.buffer = nil
	i := len(data)
	if len(res.trailer) == 0 {
		data = append(data, "0\r\n\r\n"...)
	} else {
		data = append(data, "0\r\n"...)
		i += 3
		for k, v := range res.trailer {
			data = append(data, k...)
			i += len(k)
			data = append(data, ": "...)
			data = append(data, v...)
			i += len(v)
			data = append(data, "\r\n"...)
		}
		data = append(data, "\r\n"...)
	}
	_, err = conn.Write(data)
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
