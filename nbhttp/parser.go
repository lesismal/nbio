// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"fmt"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/lesismal/nbio/mempool"
)

// Parser .
type Parser struct {
	state int8

	cache []byte

	proto string

	statusCode int
	status     string

	headerKey   string
	headerValue string

	chunkSize     int
	header        http.Header
	chunked       bool
	headerExists  bool
	contentLength int
	trailer       http.Header
	// todo
	readLimit int
	isClient  bool

	Processor Processor

	session interface{}
}

func (p *Parser) nextState(state int8) {
	p.state = state
}

// Read .
func (p *Parser) Read(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	var c byte
	var start = 0
	var offset = len(p.cache)
	if offset > 0 {
		if offset+len(data) > p.readLimit {
			return ErrTooLong
		}
		p.cache = mempool.Realloc(p.cache, offset+len(data))
		copy(p.cache[offset:], data)
		mempool.Free(data)
		data = p.cache
		p.cache = nil
	}
	for i := offset; i < len(data); i++ {
		c = data[i]
		switch p.state {
		case stateMethodBefore:
			if isValidMethodChar(c) {
				start = i
				p.nextState(stateMethod)
				continue
			}
			return ErrInvalidMethod
		case stateMethod:
			if c == ' ' {
				var method = strings.ToUpper(string(data[start:i]))
				if !isValidMethod(method) {
					return ErrInvalidMethod
				}
				p.Processor.OnMethod(method)
				start = i + 1
				p.nextState(statePathBefore)
				continue
			}
			if !isAlpha(c) {
				return ErrInvalidMethod
			}
		case statePathBefore:
			if c == '/' {
				start = i
				p.nextState(statePath)
				continue
			}
			if c != ' ' {
				return ErrInvalidRequestURI
			}
		case statePath:
			if c == ' ' {
				var uri = string(data[start:i])
				if err := p.Processor.OnURL(uri); err != nil {
					return err
				}
				start = i + 1
				p.nextState(stateProtoBefore)
			}
		case stateProtoBefore:
			if c != ' ' {
				start = i
				p.nextState(stateProto)
			}
		case stateProto:
			switch c {
			case ' ':
				if p.proto == "" {
					p.proto = string(data[start:i])
				}
			case '\r':
				if p.proto == "" {
					p.proto = string(data[start:i])
				}
				if err := p.Processor.OnProto(p.proto); err != nil {
					p.proto = ""
					return err
				}
				p.proto = ""
				p.nextState(stateProtoLF)
			}
		case stateClientProtoBefore:
			if c == 'H' {
				start = i
				p.nextState(stateClientProto)
				continue
			}
			return ErrInvalidMethod
		case stateClientProto:
			switch c {
			case ' ':
				if p.proto == "" {
					p.proto = string(data[start:i])
				}
				if err := p.Processor.OnProto(p.proto); err != nil {
					p.proto = ""
					return err
				}
				p.proto = ""
				p.nextState(stateStatusCodeBefore)
			}
		case stateStatusCodeBefore:
			switch c {
			case ' ':
			default:
				if isNum(c) {
					start = i
					p.nextState(stateStatusCode)
				}
				continue
			}
			return ErrInvalidHTTPStatusCode
		case stateStatusCode:
			if c == ' ' {
				cs := string(data[start:i])
				code, err := strconv.Atoi(cs)
				if err != nil {
					return err
				}
				p.statusCode = code
				p.nextState(stateStatusBefore)
				continue
			}
			if !isNum(c) {
				return ErrInvalidHTTPStatusCode
			}
		case stateStatusBefore:
			switch c {
			case ' ':
			default:
				if isAlpha(c) {
					start = i
					p.nextState(stateStatus)
				}
				continue
			}
			return ErrInvalidHTTPStatus
		case stateStatus:
			switch c {
			case ' ':
				if p.status == "" {
					p.status = string(data[start:i])
				}
			case '\r':
				if p.status == "" {
					p.status = string(data[start:i])
				}
				p.Processor.OnStatus(p.statusCode, p.status)
				p.statusCode = 0
				p.status = ""
				p.nextState(stateStatusLF)
			}
		case stateStatusLF:
			if c == '\n' {
				p.nextState(stateHeaderKeyBefore)
				continue
			}
			return ErrLFExpected
		case stateProtoLF:
			if c == '\n' {
				start = i + 1
				p.nextState(stateHeaderKeyBefore)
				continue
			}
			return ErrLFExpected
		case stateHeaderValueLF:
			if c == '\n' {
				start = i + 1
				p.nextState(stateHeaderKeyBefore)
				continue
			}
			return ErrLFExpected
		case stateHeaderKeyBefore:
			switch c {
			case ' ':
				if !p.headerExists {
					return ErrInvalidCharInHeader
				}
			case '\r':
				err := p.parseTransferEncoding()
				if err != nil {
					return err
				}
				err = p.parseContentLength()
				if err != nil {
					return err
				}
				p.Processor.OnContentLength(p.contentLength)
				err = p.parseTrailer()
				if err != nil {
					return err
				}
				start = i + 1
				p.nextState(stateHeaderOverLF)
			case '\n':
				return ErrInvalidCharInHeader
			default:
				if isAlpha(c) {
					start = i
					p.nextState(stateHeaderKey)
					p.headerExists = true
					continue
				}
				return ErrInvalidCharInHeader
			}
		case stateHeaderKey:
			switch c {
			case ' ':
				if p.headerKey == "" {
					p.headerKey = http.CanonicalHeaderKey(string(data[start:i]))
				}
			case ':':
				if p.headerKey == "" {
					p.headerKey = http.CanonicalHeaderKey(string(data[start:i]))
				}
				start = i + 1
				p.nextState(stateHeaderValueBefore)
			case '\r', '\n':
				return ErrInvalidCharInHeader
			default:
				if !isToken(c) {
					return ErrInvalidCharInHeader
				}
			}
		case stateHeaderValueBefore:
			switch c {
			case ' ':
			case '\r':
				if p.headerValue == "" {
					p.headerValue = string(data[start:i])
				}
				switch p.headerKey {
				case "Transfer-Encoding", "Trailer", "Content-Length":
					if p.header == nil {
						p.header = http.Header{}
					}
					p.header.Add(p.headerKey, p.headerValue)
				default:
				}

				p.Processor.OnHeader(p.headerKey, p.headerValue)
				p.headerKey = ""
				p.headerValue = ""

				start = i + 1
				p.nextState(stateHeaderValueLF)
			case '\n':
				return ErrInvalidCharInHeader
			default:
				// if !isToken(c) {
				// 	return ErrInvalidCharInHeader
				// }
				start = i
				p.nextState(stateHeaderValue)
			}
		case stateHeaderValue:
			switch c {
			case '\r':
				if p.headerValue == "" {
					p.headerValue = string(data[start:i])
				}
				switch p.headerKey {
				case "Transfer-Encoding", "Trailer", "Content-Length":
					if p.header == nil {
						p.header = http.Header{}
					}
					p.header.Add(p.headerKey, p.headerValue)
				default:
				}

				p.Processor.OnHeader(p.headerKey, p.headerValue)
				p.headerKey = ""
				p.headerValue = ""

				start = i + 1
				p.nextState(stateHeaderValueLF)
			case '\n':
				return ErrInvalidCharInHeader
			default:
			}
		case stateHeaderOverLF:
			if c == '\n' {
				p.headerExists = false
				if p.chunked {
					start = i + 1
					p.nextState(stateBodyChunkSizeBefore)
				} else {
					start = i + 1
					if p.contentLength > 0 {
						p.nextState(stateBodyContentLength)
					} else {
						p.handleMessage()
					}
				}
				continue
			}
			return ErrLFExpected
		case stateBodyContentLength:
			cl := p.contentLength
			left := len(data) - start
			if left == cl {
				var body []byte
				// data is body
				if start == 0 {
					body = data
					p.Processor.OnBody(body, true)
				} else {
					body = mempool.Malloc(cl)
					copy(body, data[start:start+cl])
					mempool.Free(data)
					p.Processor.OnBody(body, true)
				}
				p.handleMessage()
				return nil
			} else if left > cl {
				left = cl
				if left < 2048 {
					left = 2048
				}
				body := mempool.Malloc(left)[:cl]
				copy(body, data[start:start+cl])
				p.Processor.OnBody(body, true)
				p.handleMessage()
				start += cl
				i = start - 1
			} else {
				if start == 0 {
					p.cache = data
					return nil
				}
				p.cache = mempool.Malloc(cl)[:left]
				copy(p.cache, data[start:])
				mempool.Free(data)
				return nil
			}
		case stateBodyChunkSizeBefore:
			if isHex(c) {
				p.chunkSize = -1
				start = i
				p.nextState(stateBodyChunkSize)
				continue
			}
			return ErrInvalidChunkSize
		case stateBodyChunkSize:
			switch c {
			case ' ':
				if p.chunkSize < 0 {
					cs := string(data[start:i])
					chunkSize, err := strconv.ParseInt(cs, 16, 63)
					if err != nil || chunkSize < 0 {
						return fmt.Errorf("invalid chunk size %v", cs)
					}
					p.chunkSize = int(chunkSize)
				}
			case '\r':
				if p.chunkSize < 0 {
					cs := string(data[start:i])
					chunkSize, err := strconv.ParseInt(cs, 16, 63)
					if err != nil || chunkSize < 0 {
						return fmt.Errorf("invalid chunk size %v", cs)
					}
					p.chunkSize = int(chunkSize)
				}
				start = i + 1
				p.nextState(stateBodyChunkSizeLF)
			default:
				if !isHex(c) && p.chunkSize < 0 {
					cs := string(data[start:i])
					chunkSize, err := strconv.ParseInt(cs, 16, 63)
					if err != nil || chunkSize < 0 {
						return fmt.Errorf("invalid chunk size %v", cs)
					}
					p.chunkSize = int(chunkSize)
				} else {
					// chunk extension
				}
			}
		case stateBodyChunkSizeLF:
			if c == '\n' {
				start = i + 1
				if p.chunkSize > 0 {
					p.nextState(stateBodyChunkData)
				} else {
					// chunk size is 0

					if len(p.trailer) > 0 {
						// read trailer headers
						p.nextState(stateBodyTrailerHeaderKeyBefore)
					} else {
						// read tail cr lf
						p.nextState(stateTailCR)
					}
				}
				continue
			}
			return ErrLFExpected
		case stateBodyChunkData:
			cl := p.chunkSize
			left := len(data) - start
			if left > cl {
				left = cl
				if cl < 2048 {
					left = 2048
				}
				body := mempool.Malloc(left)[:cl]
				copy(body, data[start:start+cl])
				p.Processor.OnBody(body, true)
				start += cl
				i = start - 1
				p.nextState(stateBodyChunkDataCR)
			} else if left == cl {
				if start == 0 {
					p.Processor.OnBody(data, true)
				} else {
					p.Processor.OnBody(data[start:], false)
					mempool.Free(data)
				}
				p.nextState(stateBodyChunkDataCR)
				return nil
			} else {
				if start == 0 {
					p.cache = data
					return nil
				}
				if left < 2048 {
					p.cache = mempool.Malloc(2048)[:left]
				} else {
					p.cache = mempool.Malloc(left)
				}
				copy(p.cache, data[start:])
				mempool.Free(data)
				return nil
			}
		case stateBodyChunkDataCR:
			if c == '\r' {
				p.nextState(stateBodyChunkDataLF)
				continue
			}
			return ErrCRExpected
		case stateBodyChunkDataLF:
			if c == '\n' {
				p.nextState(stateBodyChunkSizeBefore)
				continue
			}
			return ErrLFExpected
		case stateBodyTrailerHeaderValueLF:
			if c == '\n' {
				start = i
				p.nextState(stateBodyTrailerHeaderKeyBefore)
				continue
			}
			return ErrLFExpected
		case stateBodyTrailerHeaderKeyBefore:
			if isAlpha(c) {
				start = i
				p.nextState(stateBodyTrailerHeaderKey)
				continue
			}

			// all trailer header readed
			if c == '\r' {
				if len(p.trailer) > 0 {
					return ErrTrailerExpected
				}
				start = i + 1
				p.nextState(stateTailLF)
				continue
			}
		case stateBodyTrailerHeaderKey:
			switch c {
			case ' ':
				if p.headerKey == "" {
					p.headerKey = http.CanonicalHeaderKey(string(data[start:i]))
				}
				continue
			case ':':
				if p.headerKey == "" {
					p.headerKey = http.CanonicalHeaderKey(string(data[start:i]))
				}
				start = i + 1
				p.nextState(stateBodyTrailerHeaderValueBefore)
				continue
			}
			if !isToken(c) {
				return ErrInvalidCharInHeader
			}
		case stateBodyTrailerHeaderValueBefore:
			switch c {
			case ' ':
			case '\r':
				if p.headerValue == "" {
					p.headerValue = string(data[start:i])
				}
				p.Processor.OnTrailerHeader(p.headerKey, p.headerValue)
				p.headerKey = ""
				p.headerValue = ""

				start = i + 1
				p.nextState(stateBodyTrailerHeaderValueLF)
			default:
				// if !isToken(c) {
				// 	return ErrInvalidCharInHeader
				// }
				start = i
				p.nextState(stateBodyTrailerHeaderValue)
			}
		case stateBodyTrailerHeaderValue:
			switch c {
			case ' ':
				if p.headerValue == "" {
					p.headerValue = string(data[start:i])
				}
			case '\r':
				if p.headerValue == "" {
					p.headerValue = string(data[start:i])
				}
				if len(p.trailer) == 0 {
					return fmt.Errorf("invalid trailer '%v'", p.headerKey)
				}
				delete(p.trailer, p.headerKey)

				p.Processor.OnTrailerHeader(p.headerKey, p.headerValue)
				start = i + 1
				p.headerKey = ""
				p.headerValue = ""
				p.nextState(stateBodyTrailerHeaderValueLF)
			default:
				// if !isToken(c) {
				// 	return ErrInvalidCharInHeader
				// }
			}
		case stateTailCR:
			if c == '\r' {
				p.nextState(stateTailLF)
				continue
			}
			return ErrCRExpected
		case stateTailLF:
			if c == '\n' {
				start = i + 1
				p.handleMessage()
				continue
			}
			return ErrLFExpected
		default:
		}
	}

	if start > 0 {
		left := len(data) - start
		if left > 0 {
			if left < 2048 {
				p.cache = mempool.Malloc(2048)[:left]
			} else {
				p.cache = mempool.Malloc(left)
			}
			copy(p.cache, data[start:])
		}
	} else {
		p.cache = data
	}
	return nil
}

// Session returns user session
func (p *Parser) Session() interface{} {
	return p.session
}

// SetSession sets user session
func (p *Parser) SetSession(session interface{}) {
	p.session = session
}

func (p *Parser) parseTransferEncoding() error {
	raw, present := p.header["Transfer-Encoding"]
	if !present {
		return nil
	}
	delete(p.header, "Transfer-Encoding")

	if len(raw) != 1 {
		return fmt.Errorf("too many transfer encodings: %q", raw)
	}
	if strings.ToLower(textproto.TrimString(raw[0])) != "chunked" {
		return fmt.Errorf("unsupported transfer encoding: %q", raw[0])
	}
	delete(p.header, "Content-Length")
	p.chunked = true

	return nil
}

func (p *Parser) parseContentLength() (err error) {
	if cl := p.header.Get("Content-Length"); cl != "" {
		if p.chunked {
			return ErrUnexpectedContentLength
		}
		end := len(cl) - 1
		for i := end; i >= 0; i-- {
			if cl[i] != ' ' {
				if i != end {
					cl = cl[:i+1]
				}
				break
			}
		}
		l, err := strconv.ParseInt(cl, 10, 63)
		if err != nil {
			return fmt.Errorf("%s %q", "bad Content-Length", cl)
		}
		if l < 0 {
			return ErrInvalidContentLength
		}
		p.contentLength = int(l)
	} else {
		p.contentLength = -1
	}
	return nil
}

func (p *Parser) parseTrailer() error {
	if !p.chunked {
		return nil
	}
	header := p.header

	trailers, ok := header["Trailer"]
	if !ok {
		return nil
	}

	header.Del("Trailer")

	trailer := http.Header{}
	for _, key := range trailers {
		key = textproto.TrimString(key)
		if key == "" {
			continue
		}
		if !strings.Contains(key, ",") {
			key = http.CanonicalHeaderKey(key)
			switch key {
			case "Transfer-Encoding", "Trailer", "Content-Length":
				return fmt.Errorf("%s %q", "bad trailer key", key)
			default:
				trailer[key] = nil
			}
			continue
		}
		for _, k := range strings.Split(key, ",") {
			if k = textproto.TrimString(k); k != "" {
				k = http.CanonicalHeaderKey(k)
				switch k {
				case "Transfer-Encoding", "Trailer", "Content-Length":
					return fmt.Errorf("%s %q", "bad trailer key", k)
				default:
					trailer[k] = nil
				}
			}
		}
	}
	if len(trailer) > 0 {
		p.trailer = trailer
	}
	return nil
}

func (p *Parser) handleMessage() {
	p.Processor.OnComplete(p)
	p.header = nil

	if !p.isClient {
		p.nextState(stateMethodBefore)
	} else {
		p.nextState(stateClientProtoBefore)
	}
}

// NewParser .
func NewParser(processor Processor, isClient bool, readLimit int) *Parser {
	if processor == nil {
		processor = NewEmptyProcessor()
	}
	state := stateMethodBefore
	if isClient {
		state = stateClientProtoBefore
	}
	return &Parser{
		state:     state,
		readLimit: readLimit,
		isClient:  isClient,
		Processor: processor,
	}
}
