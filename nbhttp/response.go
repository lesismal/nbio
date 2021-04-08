// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
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

	statusCode int // status code passed to WriteHeader
	status     string
	header     http.Header
	trailers   http.Header

	bodySize int
	bodyList [][]byte
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

// Write .
func (res *Response) Write(data []byte) (int, error) {
	res.WriteHeader(http.StatusOK)
	n := len(data)
	if n > 0 {
		if n <= 4096 {
			res.bodyList = append(res.bodyList, data)
			res.bodySize += len(data)
		} else {
			res.bodySize += len(data)

			n = 4096
			for len(data) > 0 {
				if len(data) < n {
					n = len(data)
				}
				res.bodyList = append(res.bodyList, data[:n])
				data = data[n:]
			}
		}
	}
	return len(data), nil
}

// flush .
func (res *Response) flush(conn net.Conn) error {
	res.WriteHeader(http.StatusOK)
	statusCode := res.statusCode
	status := res.status

	// res.header.Add("Poweredby", "https://github.com/lesismal/nbio")

	chunked := false
	encodingFound := false
	if res.request.ProtoAtLeast(1, 1) {
		for _, v := range res.header["Transfer-Encoding"] {
			if v == "chunked" {
				chunked = true
				encodingFound = true
			}
		}
		if !chunked {
			if len(res.header["Trailer"]) > 0 {
				chunked = true
			}
		}
	}
	if chunked {
		if !encodingFound {
			res.header.Add("Transfer-Encoding", "chunked")
		}
		res.header.Del("Content-Length")
	} else if res.bodySize > 0 {
		res.header.Add("Content-Length", strconv.Itoa(res.bodySize))
	}

	size := res.bodySize + 1024

	if size < 2048 {
		size = 2048
	}

	data := mempool.Malloc(size)

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

	trailer := map[string]string{}
	for k, vv := range res.header {
		if strings.HasPrefix(k, "Trailer-") {
			if len(vv) > 0 {
				trailer[k] = vv[0]
			}
			continue
		}
		for _, value := range vv {
			// value := strings.Join(vv, ",")
			data = mempool.Realloc(data, i+len(k)+len(value)+4+res.bodySize)
			copy(data[i:], k)
			i += len(k)
			data[i] = ':'
			i++
			data[i] = ' '
			i++
			copy(data[i:], value)
			i += len(value)
			data[i] = '\r'
			i++
			data[i] = '\n'
			i++
		}
	}

	if len(res.header["Content-Type"]) == 0 {
		const contentType = "Content-Type: text/plain; charset=utf-8\r\n"
		data = mempool.Realloc(data, i+len(contentType))
		copy(data[i:], contentType)
		i += len(contentType)
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
	i += 2

	if !chunked {
		if res.bodySize == 0 {
			_, err := conn.Write(data)
			return err
		}
		if i+res.bodySize <= 8192 {
			data = mempool.Realloc(data, i+res.bodySize)
			for _, v := range res.bodyList {
				copy(data[i:], v)
				i += len(v)
			}
			_, err := conn.Write(data)
			return err
		} else {
			data = mempool.Realloc(data, i+len(res.bodyList[0]))
			copy(data[i:], res.bodyList[0])
			_, err := conn.Write(data)
			if err != nil {
				return err
			}
			for i := 1; i < len(res.bodyList); i++ {
				v := res.bodyList[i]
				data = mempool.Malloc(len(v))
				copy(data, v)
				_, err = conn.Write(data)
				if err != nil {
					return err
				}
			}
		}
	} else {
		for _, v := range res.bodyList {
			lenStr := strconv.FormatInt(int64(len(v)), 16)
			data = mempool.Realloc(data, i+len(lenStr)+len(v)+4)
			copy(data[i:], lenStr)
			i += len(lenStr)
			copy(data[i:], "\r\n")
			i += 2
			copy(data[i:], v)
			i += len(v)
			copy(data[i:], "\r\n")
			i += 2
			if len(data) > 4096 {
				_, err := conn.Write(data)
				if err != nil {
					return err
				}
				data = mempool.Malloc(4096)[0:0]
				i = 0
			}
		}
		data = mempool.Realloc(data, i+5)
		if len(trailer) == 0 {
			copy(data[i:], "0\r\n\r\n")
		} else {
			copy(data[i:], "0\r\n")
			i += 3

			for k, v := range trailer {
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
			data = mempool.Realloc(data, i+2)
			copy(data[i:], "\r\n")
		}
		_, err := conn.Write(data)
		if err != nil {
			return err
		}
	}

	return nil
}

// NewResponse .
func NewResponse(processor Processor, request *http.Request, sequence uint64) http.ResponseWriter {
	response := responsePool.Get().(*Response)
	response.processor = processor
	response.request = request
	response.sequence = sequence
	response.header = http.Header{}
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
