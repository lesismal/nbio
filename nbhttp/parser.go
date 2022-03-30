// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"

	"github.com/lesismal/nbio/mempool"
)

const (
	transferEncodingHeader = "Transfer-Encoding"
	trailerHeader          = "Trailer"
	contentLengthHeader    = "Content-Length"

	// MaxUint .
	MaxUint = ^uint(0)
	// MaxInt .
	MaxInt = int64(int(MaxUint >> 1))
)

var emptyParsingFields = parsingFields{}

type parsingFields struct {
	proto string

	statusCode int
	status     string

	headerKey   string
	headerValue string

	header  http.Header
	trailer http.Header

	contentLength int
	chunkSize     int
	chunked       bool
	headerExists  bool
}

// Parser .
type Parser struct {
	mux sync.Mutex
	parsingFields

	cache []byte

	state    int8
	isClient bool

	readLimit int

	errClose error

	onClose func(p *Parser, err error)

	Processor Processor

	ConnState ReadCloser

	Engine *Engine

	Conn net.Conn

	Execute func(f func()) bool
}

func (p *Parser) nextState(state int8) {
	switch p.state {
	case stateClose:
	default:
		p.state = state
	}
}

// OnClose .
func (p *Parser) OnClose(h func(p *Parser, err error)) {
	p.onClose = h
}

// Close .
func (p *Parser) Close(err error) {
	p.mux.Lock()
	defer p.mux.Unlock()

	if p.state == stateClose {
		return
	}

	p.state = stateClose

	p.errClose = err

	if p.ConnState != nil {
		p.ConnState.Close(p, p.errClose)
	}
	if p.Processor != nil {
		p.Processor.Close(p, p.errClose)
	}
	if len(p.cache) > 0 {
		mempool.Free(p.cache)
	}
	if p.onClose != nil {
		p.onClose(p, err)
	}
}

func parseAndValidateChunkSize(originalStr string) (int, error) {
	chunkSize, err := strconv.ParseInt(originalStr, 16, 63)
	if err != nil {
		return -1, fmt.Errorf("chunk size parse error %v: %w", originalStr, err)
	}
	if chunkSize < 0 {
		return -1, fmt.Errorf("chunk size zero")
	}
	if chunkSize > MaxInt {
		return -1, fmt.Errorf("chunk size greater than max int %d", chunkSize)
	}
	return int(chunkSize), nil
}

// Read .
func (p *Parser) Read(data []byte) error {
	p.mux.Lock()
	defer p.mux.Unlock()

	if p.state == stateClose {
		return ErrClosed
	}

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
		p.cache = mempool.Append(p.cache, data...)
		data = p.cache
	}

UPGRADER:
	if p.ConnState != nil {
		udata := data
		if start > 0 {
			udata = data[start:]
		}
		err := p.ConnState.Read(p, udata)
		if p.cache != nil {
			mempool.Free(p.cache)
			p.cache = nil
		}
		return err
	}

	for i := offset; i < len(data); i++ {
		if p.ConnState != nil {
			goto UPGRADER
		}
		c = data[i]
		switch p.state {
		case stateClose:
			return ErrClosed
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
			switch c {
			case '/', '*':
				start = i
				p.nextState(statePath)
				continue
			}
			switch c {
			case ' ':
			default:
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
				case transferEncodingHeader, trailerHeader, contentLengthHeader:
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
				case transferEncodingHeader, trailerHeader, contentLengthHeader:
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
			if left >= cl {
				p.Processor.OnBody(data[start : start+cl])
				p.handleMessage()
				start += cl
				i = start - 1
			} else {
				goto Exit
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
					chunkSize, err := parseAndValidateChunkSize(string(data[start:i]))
					if err != nil {
						return err
					}
					p.chunkSize = chunkSize
				}
			case '\r':
				if p.chunkSize < 0 {
					chunkSize, err := parseAndValidateChunkSize(string(data[start:i]))
					if err != nil {
						return err
					}
					p.chunkSize = chunkSize
				}
				start = i + 1
				p.nextState(stateBodyChunkSizeLF)
			default:
				if !isHex(c) && p.chunkSize < 0 {
					chunkSize, err := parseAndValidateChunkSize(string(data[start:i]))
					if err != nil {
						return err
					}
					p.chunkSize = chunkSize
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
			if left >= cl {
				p.Processor.OnBody(data[start : start+cl])
				start += cl
				i = start - 1
				p.nextState(stateBodyChunkDataCR)
			} else {
				goto Exit
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

Exit:
	left := len(data) - start
	if left > 0 {
		if p.cache == nil {
			p.cache = mempool.Malloc(left)
			copy(p.cache, data[start:])
		} else if start > 0 {
			oldCache := p.cache
			p.cache = mempool.Malloc(left)
			copy(p.cache, data[start:])
			mempool.Free(oldCache)
		}
	} else if len(p.cache) > 0 {
		mempool.Free(p.cache)
		p.cache = nil
	}

	return nil
}

func (p *Parser) parseTransferEncoding() error {
	raw, present := p.header[transferEncodingHeader]
	if !present {
		return nil
	}
	delete(p.header, transferEncodingHeader)

	if len(raw) != 1 {
		return fmt.Errorf("too many transfer encodings: %q", raw)
	}
	if strings.ToLower(textproto.TrimString(raw[0])) != "chunked" {
		return fmt.Errorf("unsupported transfer encoding: %q", raw[0])
	}
	delete(p.header, contentLengthHeader)
	p.chunked = true

	return nil
}

func (p *Parser) parseContentLength() (err error) {
	if cl := p.header.Get(contentLengthHeader); cl != "" {
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
			return fmt.Errorf("length less than zero (%d): %w", l, ErrInvalidContentLength)
		}
		if l > MaxInt {
			return fmt.Errorf("length greater than maxint (%d): %w", l, ErrInvalidContentLength)
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

	trailers, ok := header[trailerHeader]
	if !ok {
		return nil
	}

	header.Del(trailerHeader)

	trailer := http.Header{}
	for _, key := range trailers {
		key = textproto.TrimString(key)
		if key == "" {
			continue
		}
		if !strings.Contains(key, ",") {
			key = http.CanonicalHeaderKey(key)
			switch key {
			case transferEncodingHeader, trailerHeader, contentLengthHeader:
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
				case transferEncodingHeader, trailerHeader, contentLengthHeader:
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
	p.parsingFields = emptyParsingFields

	if !p.isClient {
		p.nextState(stateMethodBefore)
	} else {
		p.nextState(stateClientProtoBefore)
	}
}

// NewParser .
func NewParser(processor Processor, isClient bool, readLimit int, executor func(f func()) bool) *Parser {
	if processor == nil {
		processor = NewEmptyProcessor()
	}
	state := stateMethodBefore
	if isClient {
		state = stateClientProtoBefore
	}
	if readLimit <= 0 {
		readLimit = DefaultHTTPReadLimit
	}
	if executor == nil {
		executor = func(f func()) bool {
			f()
			return true
		}
	}
	p := &Parser{
		state:     state,
		readLimit: readLimit,
		isClient:  isClient,
		Execute:   executor,
		Processor: processor,
	}
	return p
}
