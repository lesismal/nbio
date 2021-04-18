// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/lesismal/nbio/mempool"
)

var (
	requestPool = sync.Pool{
		New: func() interface{} {
			return &http.Request{}
		},
	}

	responsePool = sync.Pool{
		New: func() interface{} {
			return &Response{}
		},
	}
)

// Response represents the server side of an HTTP response.
type Response struct {
	processor Processor

	request *http.Request // request for this response

	status     string
	statusCode int // status code passed to WriteHeader

	header      http.Header
	trailer     map[string]string
	trailerSize int

	buffer       []byte
	intFormatBuf [10]byte

	chunked        bool
	chunkChecked   bool
	headEncoded    bool
	hasBody        bool
	enableSendfile bool
}

// Hijack .
func (res *Response) Hijack() (net.Conn, error) {
	if res.processor == nil {
		return nil, errors.New("nil Proccessor")
	}
	return res.processor.Conn(), nil
}

// Header .
func (res *Response) Header() http.Header {
	return res.header
}

// WriteHeader .
func (res *Response) WriteHeader(statusCode int) {
	if res.statusCode == 0 {
		status := http.StatusText(statusCode)
		if status != "" {
			res.status = status
			res.statusCode = statusCode
		}
	}
}

const maxPacketSize = 32768

// Write .
func (res *Response) Write(data []byte) (int, error) {
	l := len(data)
	conn := res.processor.Conn()
	if l == 0 || conn == nil {
		return 0, nil
	}

	res.hasBody = true

	res.checkChunked()

	if res.chunked {
		res.eoncodeHead()
		buf := res.buffer
		hl := len(buf)
		res.buffer = nil
		lenStr := res.formatInt(l, 16)
		size := hl + len(lenStr) + l + 4
		if size < maxPacketSize {
			if buf == nil {
				buf = mempool.Malloc(size)
			} else {
				buf = mempool.Realloc(buf, size)
			}
			copy(buf[hl:], lenStr)
			hl += len(lenStr)
			copy(buf[hl:], "\r\n")
			hl += 2
			copy(buf[hl:], data)
			hl += l
			copy(buf[hl:], "\r\n")
			res.buffer = buf
			return l, nil
		}
		_, err := conn.Write(buf)
		if err != nil {
			return 0, err
		}
		size -= hl
		buf = mempool.Malloc(size)
		copy(buf, lenStr)
		copy(buf[len(lenStr):], "\r\n")
		hl = len(lenStr) + 2
		copy(buf[hl:], data)
		hl += l
		copy(buf[hl:], "\r\n")
		if len(buf) < maxPacketSize {
			res.buffer = buf
			return l, nil
		}
		return conn.Write(buf)
	}

	if len(res.header["Content-Length"]) == 0 {
		res.header["Content-Length"] = []string{res.formatInt(l, 10)}
	}

	res.eoncodeHead()

	buf := res.buffer
	hl := len(buf)
	res.buffer = nil
	if hl+l >= maxPacketSize {
		_, err := conn.Write(buf)
		if err != nil {
			return 0, err
		}
		buf = mempool.Malloc(l)
		hl = 0
	} else {
		buf = mempool.Realloc(buf, hl+l)
	}
	copy(buf[hl:], data)
	return conn.Write(buf)
}

func (res *Response) ReadFrom(r io.Reader) (n int64, err error) {
	c := res.processor.Conn()
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

	res.WriteHeader(http.StatusOK)

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

	res.checkChunked()

	status := res.status
	statusCode := res.statusCode

	data := mempool.Malloc(4096)

	proto := res.request.Proto
	i := 0
	copy(data, proto)
	i += len(proto)

	data[i] = ' '
	i++
	data[i] = '0' + byte(statusCode/100)
	i++
	data[i] = '0' + byte(statusCode%100)/10
	i++
	data[i] = '0' + byte(statusCode%10)
	i++
	data[i] = ' '
	i++
	copy(data[i:], status)
	i += len(status)
	data[i] = '\r'
	i++
	data[i] = '\n'
	i++

	if res.hasBody && len(res.header["Content-Type"]) == 0 {
		const contentType = "Content-Type: text/plain; charset=utf-8\r\n"
		copy(data[i:], contentType)
		i += len(contentType)
	}
	if !res.chunked {
		if !res.hasBody {
			const contentLenthZero = "Content-Length: 0\r\n"
			copy(data[i:], contentLenthZero)
			i += len(contentLenthZero)
		}
	}
	if res.request.Close && len(res.header["Connection"]) == 0 {
		const connection = "Connection: close\r\n"
		copy(data[i:], connection)
		i += len(connection)
	}

	if len(res.header["Date"]) == 0 {
		const days = "SunMonTueWedThuFriSat"
		const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
		data = data[:i]
		t := time.Now().UTC()
		yy, mm, dd := t.Date()
		hh, mn, ss := t.Clock()
		day := days[3*t.Weekday():]
		mon := months[3*(mm-1):]
		_ = append(data[i:],
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
		i += 37
	}

	res.trailer = map[string]string{}
	trailers := res.header["Trailer"]
	for _, k := range trailers {
		res.trailer[k] = ""
	}
	for k, vv := range res.header {
		if _, ok := res.trailer[k]; !ok {
			for _, v := range vv {
				// v := strings.Join(vv, ",")
				data = mempool.Realloc(data, i+len(k)+len(v)+4)
				copy(data[i:], k)
				i += len(k)
				data[i] = ':'
				i++
				data[i] = ' '
				i++
				copy(data[i:], v)
				i += len(v)
				data[i] = '\r'
				i++
				data[i] = '\n'
				i++
			}
		} else if len(vv) > 0 {
			v := res.header.Get(k)
			res.trailer[k] = v
			res.trailerSize += (len(k) + len(v) + 4)
		}
	}

	data = mempool.Realloc(data, i+2)
	copy(data[i:], "\r\n")

	res.buffer = data
}

func (res *Response) flushTrailer(conn net.Conn) error {
	var err error

	if !res.chunked {
		if res.buffer != nil {
			_, err = conn.Write(res.buffer)
			res.buffer = nil
		}

		return err
	}

	data := res.buffer
	res.buffer = nil
	i := len(data)
	if data == nil {
		data = mempool.Malloc(res.trailerSize + 5)
	} else {
		data = mempool.Realloc(data, len(data)+res.trailerSize+5)
	}
	if len(res.trailer) == 0 {
		copy(data[i:], "0\r\n\r\n")
	} else {
		copy(data[i:], "0\r\n")
		i += 3
		for k, v := range res.trailer {
			copy(data[i:], k)
			i += len(k)
			data[i] = ':'
			i++
			data[i] = ' '
			i++
			copy(data[i:], v)
			i += len(v)
			data[i] = '\r'
			i++
			data[i] = '\n'
			i++
		}
		data = mempool.Realloc(data, i+2)
		copy(data[i:], "\r\n")
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
func NewResponse(processor Processor, request *http.Request, enableSendfile bool) *Response {
	res := responsePool.Get().(*Response)
	res.processor = processor
	res.request = request
	res.header = http.Header{"Server": []string{"nbio"}}
	res.enableSendfile = enableSendfile
	return res
}
