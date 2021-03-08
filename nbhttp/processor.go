// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"container/heap"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/loging"
	"github.com/lesismal/nbio/mempool"
)

// Processor .
type Processor interface {
	OnMethod(method string)
	OnURL(uri string) error
	OnProto(proto string) error
	OnStatus(code int, status string)
	OnHeader(key, value string)
	OnContentLength(contentLength int)
	OnBody(data []byte)
	OnTrailerHeader(key, value string)
	OnComplete(parser *Parser)
	HandleExecute(executor func(f func()))
	WriteResponse(w http.ResponseWriter)
	Clear()
}

// ServerProcessor .
type ServerProcessor struct {
	mux      sync.Mutex
	Conn     net.Conn
	request  *http.Request
	handler  http.Handler
	executor func(f func())

	resQueue     responseQueue
	sequence     uint64
	responsedSeq uint64
}

// OnMethod .
func (p *ServerProcessor) OnMethod(method string) {
	if p.request == nil {
		p.request = &http.Request{
			Method: method,
			Header: http.Header{},
		}
	} else {
		p.request.Method = method
	}
}

// OnURL .
func (p *ServerProcessor) OnURL(uri string) error {
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		return err
	}
	p.request.URL = u
	p.request.RequestURI = uri
	return nil
}

// OnProto .
func (p *ServerProcessor) OnProto(proto string) error {
	protoMajor, protoMinor, ok := http.ParseHTTPVersion(proto)
	if !ok {
		return fmt.Errorf("%s %q", "malformed HTTP version", proto)
	}
	p.request.Proto = proto
	p.request.ProtoMajor = protoMajor
	p.request.ProtoMinor = protoMinor
	return nil
}

// OnStatus .
func (p *ServerProcessor) OnStatus(code int, status string) {

}

// OnHeader .
func (p *ServerProcessor) OnHeader(key, value string) {
	values := p.request.Header.Values(key)
	i, j := 0, 0
	for i < len(value) && j < len(value) {
		for i < len(value) {
			c := value[i]
			if c != ' ' && c != ',' {
				break
			}
			i++
		}
		j = i + 1
		for j < len(value) {
			c := value[j]
			if c == ' ' || c == ',' {
				break
			}
			j++
		}
		if j <= len(value) {
			values = append(values, value[i:j])
		}
		i = j + 1
	}
	if len(values) == 0 {
		values = append(values, "")
	}
	p.request.Header[key] = values
}

// OnContentLength .
func (p *ServerProcessor) OnContentLength(contentLength int) {
	p.request.ContentLength = int64(contentLength)
}

// OnBody .
func (p *ServerProcessor) OnBody(data []byte) {
	if p.request.Body == nil {
		p.request.Body = NewBodyReader(data, 0)
	} else {
		p.request.Body.(*BodyReader).Append(data)
		mempool.Free(data)
	}
}

// OnTrailerHeader .
func (p *ServerProcessor) OnTrailerHeader(key, value string) {
	if p.request.Trailer == nil {
		p.request.Trailer = http.Header{}
	}
	p.request.Trailer.Add(key, value)
}

// OnComplete .
func (p *ServerProcessor) OnComplete(parser *Parser) {
	request := p.request
	p.request = nil

	if p.Conn != nil {
		request.RemoteAddr = p.Conn.RemoteAddr().String()
	}

	if request.URL.Host == "" {
		request.URL.Host = request.Header.Get("Host")
		request.Host = request.URL.Host
	}

	request.TransferEncoding = request.Header["Transfer-Encoding"]

	if request.ProtoMajor < 1 {
		request.Close = true
	} else {
		if request.ProtoMajor == 1 && request.ProtoMinor == 0 {
			hasClose := false
			keepAlive := false
			for _, v := range request.Header["Connection"] {
				switch strings.ToLower(v) {
				case "close":
					hasClose = true
				case "keep-alive":
					keepAlive = true
				}
			}
			request.Close = hasClose || !keepAlive
		}
	}

	if request.Body == nil {
		request.Body = &BodyReader{}
	}

	response := NewResponse(p, request, atomic.AddUint64(&p.sequence, 1))
	p.executor(func() {
		p.handler.ServeHTTP(response, request)
		p.WriteResponse(response)
		if request.Body != nil {
			request.Body.Close()
		}
		if request.Close {
			// the data may still in the send queue
			if p.Conn != nil {
				p.Conn.Close()
			}
		} else {
			if p.Conn != nil {
				p.Conn.SetReadDeadline(time.Now().Add(time.Second * 120))
			}
		}
	})

}

// HandleExecute .
func (p *ServerProcessor) HandleExecute(executor func(f func())) {
	if executor != nil {
		p.executor = executor
	}
}

// WriteResponse .
func (p *ServerProcessor) WriteResponse(w http.ResponseWriter) {
	if p.Conn != nil {
		res, ok := w.(*Response)
		if ok {
			p.mux.Lock()
			defer p.mux.Unlock()
			heap.Push(&p.resQueue, res)
			for len(p.resQueue) > 0 {
				res = p.resQueue[0]
				if res.sequence != (p.responsedSeq + 1) {
					return
				}
				p.Conn.Write(res.encode())
				heap.Remove(&p.resQueue, res.index)
				p.responsedSeq++
			}
		}
	}
}

// Clear .
func (p *ServerProcessor) Clear() {

}

// HandleMessage .
func (p *ServerProcessor) HandleMessage(handler http.Handler) {
	if handler != nil {
		p.handler = handler
	}
}

func (p *ServerProcessor) call(f func()) {
	defer func() {
		if err := recover(); err != nil {
			loging.Error("ServerProcessor call failed: %v", err)
			debug.PrintStack()
		}
	}()
	f()
}

// NewServerProcessor .
func NewServerProcessor(conn net.Conn, handler http.Handler) Processor {
	if handler == nil {
		panic(errors.New("invalid handler for ServerProcessor: nil"))
	}
	return &ServerProcessor{
		// Allocator: &Allocator{},
		// TaskPool:  NewTaskPool(20000),
		Conn:     conn,
		handler:  handler,
		executor: func(f func()) { f() },
	}
}

// ClientProcessor .
type ClientProcessor struct {
	response *http.Response
	handler  func(*http.Response)
}

// OnMethod .
func (p *ClientProcessor) OnMethod(method string) {
}

// OnURL .
func (p *ClientProcessor) OnURL(uri string) error {
	return nil
}

// OnProto .
func (p *ClientProcessor) OnProto(proto string) error {
	protoMajor, protoMinor, ok := http.ParseHTTPVersion(proto)
	if !ok {
		return fmt.Errorf("%s %q", "malformed HTTP version", proto)
	}
	if p.response == nil {
		p.response = &http.Response{
			Proto:  proto,
			Header: http.Header{},
		}
	} else {
		p.response.Proto = proto
	}
	p.response.ProtoMajor = protoMajor
	p.response.ProtoMinor = protoMinor
	return nil
}

// OnStatus .
func (p *ClientProcessor) OnStatus(code int, status string) {
	p.response.StatusCode = code
	p.response.Status = status
}

// OnHeader .
func (p *ClientProcessor) OnHeader(key, value string) {
	p.response.Header.Add(key, value)
}

// OnContentLength .
func (p *ClientProcessor) OnContentLength(contentLength int) {
	p.response.ContentLength = int64(contentLength)
}

// OnBody .
func (p *ClientProcessor) OnBody(data []byte) {
	if p.response.Body == nil {
		p.response.Body = NewBodyReader(data, 0)
	} else {
		p.response.Body.(*BodyReader).Append(data)
		mempool.Free(data)
	}
}

// OnTrailerHeader .
func (p *ClientProcessor) OnTrailerHeader(key, value string) {
	if p.response.Trailer == nil {
		p.response.Trailer = http.Header{}
	}
	p.response.Trailer.Add(key, value)
}

// OnComplete .
func (p *ClientProcessor) OnComplete(parser *Parser) {
	p.handler(p.response)
	p.response = nil
}

// HandleExecute .
func (p *ClientProcessor) HandleExecute(executor func(f func())) {

}

// WriteResponse .
func (p *ClientProcessor) WriteResponse(response http.ResponseWriter) {

}

// Clear .
func (p *ClientProcessor) Clear() {

}

// HandleMessage .
func (p *ClientProcessor) HandleMessage(handler func(*http.Response)) {
	if handler != nil {
		p.handler = handler
	}
}

// NewClientProcessor .
func NewClientProcessor(handler func(*http.Response)) Processor {
	if handler == nil {
		panic(errors.New("invalid handler for ClientProcessor: nil"))
	}
	return &ClientProcessor{
		handler: handler,
	}
}

// EmptyProcessor .
type EmptyProcessor struct{}

// OnMethod .
func (p *EmptyProcessor) OnMethod(method string) {

}

// OnURL .
func (p *EmptyProcessor) OnURL(uri string) error {
	return nil
}

// OnProto .
func (p *EmptyProcessor) OnProto(proto string) error {
	return nil
}

// OnStatus .
func (p *EmptyProcessor) OnStatus(code int, status string) {

}

// OnHeader .
func (p *EmptyProcessor) OnHeader(key, value string) {

}

// OnContentLength .
func (p *EmptyProcessor) OnContentLength(contentLength int) {

}

// OnBody .
func (p *EmptyProcessor) OnBody(data []byte) {

}

// OnTrailerHeader .
func (p *EmptyProcessor) OnTrailerHeader(key, value string) {

}

// OnComplete .
func (p *EmptyProcessor) OnComplete(parser *Parser) {

}

// HandleExecute .
func (p *EmptyProcessor) HandleExecute(executor func(f func())) {

}

// WriteResponse .
func (p *EmptyProcessor) WriteResponse(response http.ResponseWriter) {

}

// Clear .
func (p *EmptyProcessor) Clear() {

}

// HandleMessage .
func (p *EmptyProcessor) HandleMessage(handler http.Handler) {

}

// NewEmptyProcessor .
func NewEmptyProcessor() Processor {
	return &EmptyProcessor{
		// Allocator: &Allocator{},
		// TaskPool:  NewTaskPool(20000),
	}
}
