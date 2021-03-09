// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lesismal/nbio/mempool"
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
	if len(data) > 0 {
		res.bodyList = append(res.bodyList, data)
		res.bodySize += len(data)
	}
	return len(data), nil
}

// finish .
func (res *Response) encode() []byte {
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

	size := res.bodySize

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

		value := strings.Join(vv, ",")
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
	copy(data[i:], "\r\n")
	i += 2

	if res.bodySize == 0 {
		return data
	}

	if !chunked {
		data = mempool.Realloc(data, i+res.bodySize)
		for _, v := range res.bodyList {
			copy(data[i:], v)
			i += len(v)
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
	}

	return data
}

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
