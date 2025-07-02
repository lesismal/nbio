// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lesismal/nbio/mempool"
)

var (
	// used to reset a http.Request to empty value.
	emptyRequest = http.Request{}
	// used to reset a Response to empty value.
	emptyResponse = Response{}
	// used to reset a http.Response to empty value.
	emptyClientResponse = http.Response{}

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

	clientResponsePool = sync.Pool{
		New: func() interface{} {
			return &http.Response{}
		},
	}
)

//go:norace
func releaseRequest(req *http.Request, retainHTTPBody bool) {
	if req != nil {
		if req.Body != nil {
			if br, ok := req.Body.(*BodyReader); ok {
				if retainHTTPBody {
					// do not release the body
				} else {
					_ = br.Close()
					*br = emptyBodyReader
					bodyReaderPool.Put(br)
				}
			} else if !retainHTTPBody {
				_ = req.Body.Close()
			}
		}
		// fast gc for fields
		*req = emptyRequest
		requestPool.Put(req)
	}
}

//go:norace
func releaseResponse(res *Response) {
	if res != nil {
		if res.buffer != nil {
			mempool.Free(res.buffer)
		}
		if res.bodyBuffer != nil {
			mempool.Free(res.bodyBuffer)
		}
		*res = emptyResponse
		responsePool.Put(res)
	}
}

//go:norace
func releaseClientResponse(res *http.Response) {
	if res != nil {
		if res.Body != nil {
			br := res.Body.(*BodyReader)
			_ = br.Close()
			*br = emptyBodyReader
			bodyReaderPool.Put(br)
		}
		*res = emptyClientResponse
		clientResponsePool.Put(res)
	}
}

// Processor .
type Processor interface {
	OnMethod(parser *Parser, method string)
	OnURL(parser *Parser, uri string) error
	OnProto(parser *Parser, proto string) error
	OnStatus(parser *Parser, code int, status string)
	OnHeader(parser *Parser, key, value string)
	OnContentLength(parser *Parser, contentLength int)
	OnBody(parser *Parser, data []byte) error
	OnTrailerHeader(parser *Parser, key, value string)
	OnComplete(parser *Parser)
	Close(parser *Parser, err error)
	Clean(parser *Parser)
}

var (
	emptyServerProcessor = ServerProcessor{}
	emptyClientProcessor = ClientProcessor{}
)

// ServerProcessor is used for server side connection.
type ServerProcessor struct {
	request *http.Request
}

// OnMethod .
//
//go:norace
func (p *ServerProcessor) OnMethod(parser *Parser, method string) {
	if p.request == nil {
		p.request = requestPool.Get().(*http.Request)
		if parser != nil {
			*p.request = *parser.Engine.emptyRequest
		}
		p.request.Method = method
		p.request.Header = http.Header{}
	} else {
		p.request.Method = method
	}
}

// OnURL .
//
//go:norace
func (p *ServerProcessor) OnURL(parser *Parser, rawurl string) error {
	p.request.RequestURI = rawurl

	justAuthority := p.request.Method == "CONNECT" && !strings.HasPrefix(rawurl, "/")
	if justAuthority {
		rawurl = "http://" + rawurl
	}

	u, err := url.ParseRequestURI(rawurl)
	if err != nil {
		return err
	}
	if justAuthority {
		u.Scheme = ""
	}

	p.request.URL = u
	return nil
}

// OnProto .
//
//go:norace
func (p *ServerProcessor) OnProto(parser *Parser, proto string) error {
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
//
//go:norace
func (p *ServerProcessor) OnStatus(parser *Parser, code int, status string) {

}

// OnHeader .
//
//go:norace
func (p *ServerProcessor) OnHeader(parser *Parser, key, value string) {
	values := p.request.Header[key]
	values = append(values, value)
	p.request.Header[key] = values
	// p.isUpgrade = (key == "Connection" && value == "upgrade")
}

// OnContentLength .
//
//go:norace
func (p *ServerProcessor) OnContentLength(parser *Parser, contentLength int) {
	p.request.ContentLength = int64(contentLength)
}

// OnBody .
//
//go:norace
func (p *ServerProcessor) OnBody(parser *Parser, data []byte) error {
	if p.request.Body == nil {
		p.request.Body = NewBodyReader(parser.Engine)
	}
	return p.request.Body.(*BodyReader).append(data)
}

// OnTrailerHeader .
//
//go:norace
func (p *ServerProcessor) OnTrailerHeader(parser *Parser, key, value string) {
	if p.request.Trailer == nil {
		p.request.Trailer = http.Header{}
	}
	p.request.Trailer.Add(key, value)
}

// OnComplete .
//
//go:norace
func (p *ServerProcessor) OnComplete(parser *Parser) {
	request := p.request
	p.request = nil

	if request == nil {
		return
	}

	engine := parser.Engine
	conn := parser.Conn
	request.RemoteAddr = conn.RemoteAddr().String()
	if parser.Engine.WriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(engine.WriteTimeout))
	}

	if request.URL.Host == "" {
		request.URL.Host = request.Header.Get("Host")
		request.Host = request.URL.Host
	}

	request.TransferEncoding = request.Header[transferEncodingHeader]

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
		request.Body = NewBodyReader(engine)
	}

	response := NewResponse(parser, request)

	if engine.OnRequest != nil {
		engine.OnRequest(response, request)
	}
	if !parser.Execute(func() {
		engine.Handler.ServeHTTP(response, request)
		p.flushResponse(parser, response)
	}) {
		releaseRequest(request, engine.RetainHTTPBody)
	}
}

//go:norace
func (p *ServerProcessor) flushResponse(parser *Parser, res *Response) {
	conn := parser.Conn
	engine := parser.Engine
	if conn != nil {
		req := res.request
		if !res.hijacked {
			res.WriteHeader(http.StatusOK)
			res.checkChunked()
			res.eoncodeHead()
			if err := res.flush(conn); err != nil {
				_ = conn.Close()
				releaseRequest(req, engine.RetainHTTPBody)
				releaseResponse(res)
				return
			}
			if req.Close {
				// the data may still in the send queue
				_ = conn.Close()
			} else if parser.ParserCloser == nil {
				_ = conn.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
			}
		}
		releaseRequest(req, engine.RetainHTTPBody)
		releaseResponse(res)
	}
}

// Clean .
//
//go:norace
func (p *ServerProcessor) Clean(parser *Parser) {
	if p.request != nil {
		releaseRequest(p.request, parser.Engine.RetainHTTPBody)
		p.request = nil
	}
	*p = emptyServerProcessor
}

// Close .
//
//go:norace
func (p *ServerProcessor) Close(parser *Parser, err error) {
	p.Clean(parser)
}

// NewServerProcessor .
//
//go:norace
func NewServerProcessor() Processor {
	return &ServerProcessor{}
}

// ClientProcessor is used for client side connection.
type ClientProcessor struct {
	conn     *ClientConn
	response *http.Response
	handler  func(res *http.Response, err error)
}

// OnMethod .
//
//go:norace
func (p *ClientProcessor) OnMethod(parser *Parser, method string) {
}

// OnURL .
//
//go:norace
func (p *ClientProcessor) OnURL(parser *Parser, uri string) error {
	return nil
}

// OnProto .
//
//go:norace
func (p *ClientProcessor) OnProto(parser *Parser, proto string) error {
	protoMajor, protoMinor, ok := http.ParseHTTPVersion(proto)
	if !ok {
		return fmt.Errorf("%s %q", "malformed HTTP version", proto)
	}
	if p.response == nil {
		p.response = clientResponsePool.Get().(*http.Response)
		p.response.Proto = proto
		p.response.Header = http.Header{}
	} else {
		p.response.Proto = proto
	}
	p.response.ProtoMajor = protoMajor
	p.response.ProtoMinor = protoMinor
	return nil
}

// OnStatus .
//
//go:norace
func (p *ClientProcessor) OnStatus(parser *Parser, code int, status string) {
	p.response.StatusCode = code
	p.response.Status = status
}

// OnHeader .
//
//go:norace
func (p *ClientProcessor) OnHeader(parser *Parser, key, value string) {
	p.response.Header.Add(key, value)
}

// OnContentLength .
//
//go:norace
func (p *ClientProcessor) OnContentLength(parser *Parser, contentLength int) {
	p.response.ContentLength = int64(contentLength)
}

// OnBody .
//
//go:norace
func (p *ClientProcessor) OnBody(parser *Parser, data []byte) error {
	if p.response.Body == nil {
		p.response.Body = NewBodyReader(parser.Engine)
	}
	return p.response.Body.(*BodyReader).append(data)
}

// OnTrailerHeader .
//
//go:norace
func (p *ClientProcessor) OnTrailerHeader(parser *Parser, key, value string) {
	if p.response.Trailer == nil {
		p.response.Trailer = http.Header{}
	}
	p.response.Trailer.Add(key, value)
}

// OnComplete .
//
//go:norace
func (p *ClientProcessor) OnComplete(parser *Parser) {
	res := p.response
	p.response = nil

	// Fix #225
	// Handle upgrade handshake response in the io goroutine to avoid concurrent issue:
	// 1. when the server may send a message together with handshake response
	// 2. we handle the handshake response in another goroutine
	// 3. poller continue reading data using http parser(the upgrader reader hasn't been set before 2)
	// then we got parsing errors or panic.
	if res.StatusCode == http.StatusSwitchingProtocols {
		p.handler(res, nil)
		releaseClientResponse(res)
		return
	}

	if !parser.Execute(func() {
		p.handler(res, nil)
		releaseClientResponse(res)
	}) {
		releaseClientResponse(res)
	}
}

// Clean .
//
//go:norace
func (p *ClientProcessor) Clean(parser *Parser) {
	if p.response != nil {
		releaseClientResponse(p.response)
	}
	*p = emptyClientProcessor
}

// Close .
//
//go:norace
func (p *ClientProcessor) Close(parser *Parser, err error) {
	p.conn.CloseWithError(err)
	p.Clean(parser)
}

// NewClientProcessor .
//
//go:norace
func NewClientProcessor(conn *ClientConn, handler func(res *http.Response, err error)) Processor {
	return &ClientProcessor{
		conn:    conn,
		handler: handler,
	}
}

// EmptyProcessor .
type EmptyProcessor struct{}

// OnMethod .
//
//go:norace
func (p *EmptyProcessor) OnMethod(parser *Parser, method string) {

}

// OnURL .
//
//go:norace
func (p *EmptyProcessor) OnURL(parser *Parser, uri string) error {
	return nil
}

// OnProto .
//
//go:norace
func (p *EmptyProcessor) OnProto(parser *Parser, proto string) error {
	return nil
}

// OnStatus .
//
//go:norace
func (p *EmptyProcessor) OnStatus(parser *Parser, code int, status string) {

}

// OnHeader .
//
//go:norace
func (p *EmptyProcessor) OnHeader(parser *Parser, key, value string) {

}

// OnContentLength .
//
//go:norace
func (p *EmptyProcessor) OnContentLength(parser *Parser, contentLength int) {

}

// OnBody .
//
//go:norace
func (p *EmptyProcessor) OnBody(parser *Parser, data []byte) error {
	return nil
}

// OnTrailerHeader .
//
//go:norace
func (p *EmptyProcessor) OnTrailerHeader(parser *Parser, key, value string) {

}

// OnComplete .
//
//go:norace
func (p *EmptyProcessor) OnComplete(parser *Parser) {

}

// Clean .
//
//go:norace
func (p *EmptyProcessor) Clean(parser *Parser) {

}

// Close .
//
//go:norace
func (p *EmptyProcessor) Close(parser *Parser, err error) {

}

// NewEmptyProcessor .
//
//go:norace
func NewEmptyProcessor() Processor {
	return &EmptyProcessor{}
}
