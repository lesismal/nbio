// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lesismal/nbio/logging"
)

var (
	emptyRequest  = http.Request{}
	emptyResponse = Response{}

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

	// serverProcessorPool = sync.Pool{
	// 	New: func() interface{} {
	// 		return &ServerProcessor{}
	// 	},
	// }
)

func releaseRequest(req *http.Request) {
	if req != nil {
		if req.Body != nil {
			br := req.Body.(*BodyReader)
			br.close()
			bodyReaderPool.Put(br)
		}
		// fast gc for fields
		*req = emptyRequest
		requestPool.Put(req)
	}
}

func releaseResponse(res *Response) {
	if res != nil {
		*res = emptyResponse
		responsePool.Put(res)
	}
}

// Processor .
type Processor interface {
	Conn() net.Conn
	OnMethod(method string)
	OnURL(uri string) error
	OnProto(proto string) error
	OnStatus(code int, status string)
	OnHeader(key, value string)
	OnContentLength(contentLength int)
	OnBody(data []byte)
	OnTrailerHeader(key, value string)
	OnComplete(parser *Parser)
	Close(p *Parser, err error)
}

// ServerProcessor .
type ServerProcessor struct {
	// active int32

	// mux     sync.Mutex
	conn    net.Conn
	parser  *Parser
	request *http.Request
	handler http.Handler
	// executor func(index int, f func())

	// resQueue       []*Response
	keepaliveTime  time.Duration
	enableSendfile bool
	// isUpgrade      bool
	remoteAddr string
}

// Conn .
func (p *ServerProcessor) Conn() net.Conn {
	return p.conn
}

// OnMethod .
func (p *ServerProcessor) OnMethod(method string) {
	if p.request == nil {
		p.request = requestPool.Get().(*http.Request)
		*p.request = *p.parser.Engine.emptyRequest
		p.request.Method = method
		p.request.Header = http.Header{}
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
	values := p.request.Header[key]
	values = append(values, value)
	p.request.Header[key] = values
	// p.isUpgrade = (key == "Connection" && value == "upgrade")
}

// OnContentLength .
func (p *ServerProcessor) OnContentLength(contentLength int) {
	p.request.ContentLength = int64(contentLength)
}

// OnBody .
func (p *ServerProcessor) OnBody(data []byte) {
	if p.request.Body == nil {
		p.request.Body = NewBodyReader(data)
	} else {
		p.request.Body.(*BodyReader).Append(data)
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
	// p.mux.Lock()
	request := p.request
	p.request = nil
	// p.mux.Unlock()

	if request == nil {
		return
	}

	if p.conn != nil {
		request.RemoteAddr = p.remoteAddr
	}

	if request.URL.Host == "" {
		request.URL.Host = request.Header.Get("Host")
		request.Host = request.URL.Host
	}

	request.TransferEncoding = request.Header["Transfer-Encoding"]

	if request.ProtoMajor < 1 {
		request.Close = true
	} else {
		hasClose := false
		keepAlive := false
	CONNECTION_VALUES:
		for _, v := range request.Header["Connection"] {
			switch strings.ToLower(strings.Trim(v, " ")) {
			case "close":
				hasClose = true
				break CONNECTION_VALUES
			case "keep-alive":
				keepAlive = true
			}
		}
		if request.ProtoMajor == 1 && request.ProtoMinor == 0 {
			request.Close = hasClose || !keepAlive
		} else {
			request.Close = hasClose
		}
	}

	// http 2.0
	// if request.Method == "PRI" && len(request.Header) == 0 && request.URL.Path == "*" && request.Proto == "HTTP/2.0" {
	// 	p.isUpgrade = true
	// 	p.parser.Upgrader = &Http2Upgrader{}
	// 	return
	// }

	if request.Body == nil {
		request.Body = NewBodyReader(nil)
	}

	response := NewResponse(p.parser, request, p.enableSendfile)
	parser.Execute(func() {
		p.handler.ServeHTTP(response, request)
		err := p.flushResponse(response)
		if err != nil {
			logging.Error("failed to flush http response: %v", err)
		}
	})
}

func (p *ServerProcessor) flushResponse(res *Response) error {
	var err error
	if p.conn != nil {
		req := res.request
		if !res.hijacked {
			res.eoncodeHead()
			if err := res.flushTrailer(p.conn); err != nil {
				p.conn.Close()
				releaseRequest(req)
				releaseResponse(res)
				return nil
			}
		}
		if req.Close {
			// the data may still in the send queue
			p.conn.Close()
		} else if p.parser == nil || p.parser.Upgrader == nil {
			err = p.conn.SetReadDeadline(time.Now().Add(p.keepaliveTime))
		}
		releaseRequest(req)
		releaseResponse(res)
	}
	return err
}

// Close .
func (p *ServerProcessor) Close(parser *Parser, err error) {

}

// NewServerProcessor .
func NewServerProcessor(conn net.Conn, handler http.Handler, keepaliveTime time.Duration, enableSendfile bool) Processor {
	if handler == nil {
		panic(errors.New("invalid handler for ServerProcessor: nil"))
	}
	// p := serverProcessorPool.Get().(*ServerProcessor)
	// p.conn = conn
	// p.handler = handler
	// p.executor = executor
	// p.keepaliveTime = keepaliveTime
	// p.enableSendfile = enableSendfile
	// p.remoteAddr = conn.RemoteAddr().String()
	p := &ServerProcessor{
		conn:           conn,
		handler:        handler,
		keepaliveTime:  keepaliveTime,
		enableSendfile: enableSendfile,
	}
	if conn != nil {
		p.remoteAddr = conn.RemoteAddr().String()
	}

	return p
}

// ClientProcessor .
type ClientProcessor struct {
	conn     *httpConn
	response *http.Response
	handler  func(res *http.Response, err error)
}

// Conn .
func (p *ClientProcessor) Conn() net.Conn {
	return p.conn.conn
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
		p.response.Body = NewBodyReader(data)
	} else {
		p.response.Body.(*BodyReader).Append(data)
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
	p.handler(p.response, nil)
	p.response = nil
}

// Close .
func (p *ClientProcessor) Close(parser *Parser, err error) {
	p.conn.CloseWithError(err)
}

// NewClientProcessor .
func NewClientProcessor(conn *httpConn, handler func(res *http.Response, err error)) Processor {
	return &ClientProcessor{
		conn:    conn,
		handler: handler,
	}
}

// EmptyProcessor .
type EmptyProcessor struct{}

// Conn .
func (p *EmptyProcessor) Conn() net.Conn {
	return nil
}

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

// Close .
func (p *EmptyProcessor) Close(parser *Parser, err error) {

}

// NewEmptyProcessor .
func NewEmptyProcessor() Processor {
	return &EmptyProcessor{}
}
