// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"errors"
	"net"
	"net/http"
	"strconv"
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

	index    int
	sequence uint64
	request  *http.Request // request for this response

	status     string
	statusCode int // status code passed to WriteHeader

	header      http.Header
	trailer     map[string]string
	trailerSize int
	// contentLength int

	buffer       []byte
	chunked      bool
	chunkChecked bool
	headEncoded  bool
	hasBody      bool
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
		lenStr := strconv.FormatInt(int64(l), 16)
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
		res.header["Content-Length"] = []string{strconv.FormatInt(int64(l), 10)}
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

// checkChunked .
func (res *Response) checkChunked() error {
	if res.chunkChecked {
		return nil
	}

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

	res.chunkChecked = true

	return nil
}

// flush .
func (res *Response) eoncodeHead() {
	if res.headEncoded {
		return
	}

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

	res.trailer = map[string]string{}
	trailers := res.header["Trailer"]
	for _, k := range trailers {
		res.trailer[k] = ""
	}
	for k, vv := range res.header {
		// if k != "Trailer" {
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
		// }
	}

	if len(res.header["Content-Type"]) == 0 {
		const contentType = "Content-Type: text/plain; charset=utf-8\r\n"
		data = mempool.Realloc(data, i+len(contentType))
		copy(data[i:], contentType)
		i += len(contentType)
	}
	if !res.chunked {
		if !res.hasBody {
			const contentLenthZero = "Content-Length: 0\r\n"
			data = mempool.Realloc(data, i+len(contentLenthZero))
			copy(data[i:], contentLenthZero)
			i += len(contentLenthZero)
		}
	}

	if len(res.header["Date"]) == 0 {
		const days = "SunMonTueWedThuFriSat"
		const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
		data = mempool.Realloc(data, i+37)[:i]
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

	data = mempool.Realloc(data, i+2)
	copy(data[i:], "\r\n")

	res.buffer = data

	res.headEncoded = true
}

func (res *Response) flushTrailer(conn net.Conn) error {
	var err error

	if !res.chunked {
		if !res.hasBody {
			res.header["Content-Length"] = []string{"0"}
		}

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

// NewResponse .
func NewResponse(processor Processor, request *http.Request, sequence uint64) *Response {
	response := responsePool.Get().(*Response)
	response.processor = processor
	response.request = request
	response.sequence = sequence
	response.header = http.Header{"Server": []string{"nbio"}}
	return response
}

type responseQueue []*Response

func (h responseQueue) Len() int           { return len(h) }
func (h responseQueue) Less(i, j int) bool { return h[i].sequence < h[j].sequence }
func (h responseQueue) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *responseQueue) Push(x interface{}) {
	*h = append(*h, x.(*Response))
	n := len(*h)
	(*h)[n-1].index = n - 1
}
func (h *responseQueue) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = nil // avoid memory leak
	*h = old[0 : n-1]
	return x
}
