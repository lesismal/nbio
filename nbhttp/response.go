// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"net/http"
	"strconv"

	"github.com/lesismal/nbio/mempool"
)

// Response represents the server side of an HTTP response.
// todo:
type Response struct {
	processor Processor

	index    int
	sequence uint64
	request  *http.Request // request for this response

	statusCode int // status code passed to WriteHeader
	status     string
	header     http.Header
	trailers   http.Header

	head []byte
	body []byte
}

// Header .
func (response *Response) Header() http.Header {
	return response.header
}

func (response *Response) reallocHead(size int) {
	response.head = mempool.Realloc(response.head, size)
}

func (response *Response) reallocBody(size int) {
	response.body = mempool.Realloc(response.body, size)
}

// WriteHeader .
func (response *Response) WriteHeader(statusCode int) {
	if response.statusCode == 0 {
		status := http.StatusText(statusCode)
		if status != "" {
			response.status = status
			response.statusCode = statusCode
		}
	}
}

// Write .
func (response *Response) Write(data []byte) (int, error) {
	response.WriteHeader(http.StatusOK)
	if len(data) > 0 {
		response.Header().Set("Transfer-Encoding", "chunked")

		l := len(response.body)
		lenStr := strconv.FormatInt(int64(len(data)), 16)
		size := l + len(lenStr) + 4
		if size < 2048 {
			size = 2048
		}
		response.reallocBody(size)
		for i := 0; i < len(lenStr); i++ {
			response.body[l] = lenStr[i]
			l++
		}
		response.body[l] = '\r'
		l++
		response.body[l] = '\n'
		l++
		copy(response.body[l:], data)
		l += len(data)
		response.body[l] = '\r'
		l++
		response.body[l] = '\n'
		l++
		response.body = response.body[:l]
	}
	return len(data), nil
}

// finish .
func (response *Response) finish() {
	response.WriteHeader(http.StatusOK)
	statusCode := response.statusCode
	status := response.status

	response.head = mempool.Malloc(4096)

	i := 0
	proto := response.request.Proto
	for i < len(proto) {
		response.head[i] = proto[i]
		i++
	}

	response.head[i] = ' '
	i++

	response.head[i] = '0' + byte(statusCode/100)
	i++
	response.head[i] = '0' + byte(statusCode%100)/10
	i++
	response.head[i] = '0' + byte(statusCode%10)
	i++

	response.head[i] = ' '
	i++

	for j := 0; j < len(status); j++ {
		response.head[i] = status[j]
		i++
	}

	response.head[i] = '\r'
	i++
	response.head[i] = '\n'
	i++

	for k, vv := range response.header {
		response.reallocHead(i + len(k) + 2)
		for x := 0; x < len(k); x++ {
			response.head[i] = k[x]
			i++
		}
		response.head[i] = ':'
		i++
		response.head[i] = ' '
		i++
		first := true
		for _, v := range vv {
			if first {
				first = false
				response.reallocHead(i + len(v))
			} else {
				response.reallocHead(i + len(v) + 1)
				response.head[i] = ','
				i++
			}
			for y := 0; y < len(v); y++ {
				response.head[i] = v[y]
				i++
			}
		}
		response.reallocHead(i + 2)
		response.head[i] = '\r'
		i++
		response.head[i] = '\n'
		i++
	}

	l := len(response.body)
	if l == 0 {
		response.reallocHead(i + 4)
		response.head[i] = '\r'
		response.head[i+1] = '\n'
		response.head[i+2] = '\r'
		response.head[i+3] = '\n'
		return
	}

	response.reallocHead(i + 2)
	response.head[i] = '\r'
	response.head[i+1] = '\n'
	// response.head = response.head[:i]

	// append CRLF to body as tail

	if l > 0 {
		response.reallocBody(l + 5)
		response.body[l] = '0'
		response.body[l+1] = '\r'
		response.body[l+2] = '\n'
		response.body[l+3] = '\r'
		response.body[l+4] = '\n'
	} else {
		response.reallocBody(2)
		response.body[0] = '\r'
		response.body[1] = '\n'
	}
	// fmt.Println("--------------------------------")
	// fmt.Println("write:")
	// fmt.Printf("head: [%s]\n", string(response.head))
	// fmt.Printf("body: [%s]\n", string(response.body))
	// fmt.Println("--------------------------------")
}

// Flush .
// func (response *Response) Flush() {
// 	response.processor.WriteResponse(response)
// }

// NewResponse .
func NewResponse(processor Processor, request *http.Request, sequence uint64) http.ResponseWriter {
	response := &Response{
		processor: processor,
		request:   request,
		sequence:  sequence,
		header:    http.Header{},
	}
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
