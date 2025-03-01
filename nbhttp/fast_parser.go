// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lesismal/nbio/mempool"
)

// FastParserState represents the parsing state of FastParser.
type FastParserState int

// FastParser state constants.
const (
	fastStateMethodStart FastParserState = iota
	fastStateMethod
	fastStatePathStart
	fastStatePath
	fastStateProtoStart
	fastStateProto
	fastStateHeaderStart
	fastStateHeaderName
	fastStateHeaderValueStart
	fastStateHeaderValue
	fastStateHeaderValueEnd
	fastStateBodyStart
	fastStateBody
	fastStateChunkSizeStart
	fastStateChunkSize
	fastStateChunkData
	fastStateChunkDataEnd
	fastStateComplete
	fastStateError
)

// FastParser is a high-performance HTTP parser.
// It uses state machine and zero-copy techniques to improve parsing efficiency.
type FastParser struct {
	// Parsing state
	state FastParserState
	// Request method
	method []byte
	// Request path
	path []byte
	// Request protocol
	proto []byte
	// Request headers
	headers [][2][]byte
	// Request body
	body []byte
	// Content length
	contentLength int
	// Whether to keep connection alive
	keepAlive bool
	// Whether using chunked transfer
	chunked bool
	// Current chunk size
	chunkSize int
	// Current chunk read size
	chunkRead int
	// Whether reading is complete
	complete bool
	// Error information
	err error
	// Parsing start time
	startTime time.Time
	// Parsing duration
	parseTime time.Duration
	// Memory allocator
	allocator mempool.Allocator
	// Request object pool
	requestPool sync.Pool

	// Fields required by ParserCloser interface
	conn      net.Conn
	engine    *Engine
	processor Processor
	execute   func(f func()) bool

	// Cached bytes
	bytesCached *[]byte

	// Close callback
	onClose func(p *Parser, err error)
}

//nolint:unused // Unused in this file, but exported for use in other files.
var (
	methodGet     = []byte("GET")
	methodPost    = []byte("POST")
	methodPut     = []byte("PUT")
	methodDelete  = []byte("DELETE")
	methodHead    = []byte("HEAD")
	methodOptions = []byte("OPTIONS")
	methodPatch   = []byte("PATCH")
)

// Common HTTP headers

//nolint:unused // Unused in this file, but exported for use in other files.
var (
	headerContentLength    = []byte("Content-Length")
	headerContentType      = []byte("Content-Type")
	headerConnection       = []byte("Connection")
	headerTransferEncoding = []byte("Transfer-Encoding")
	headerHost             = []byte("Host")
	headerUserAgent        = []byte("User-Agent")
	headerAccept           = []byte("Accept")
	headerAcceptEncoding   = []byte("Accept-Encoding")
	headerAcceptLanguage   = []byte("Accept-Language")
)

// Common HTTP header values.
var (
	valueChunked   = []byte("chunked")
	valueClose     = []byte("close")
	valueKeepAlive = []byte("keep-alive")
)

// Common HTTP protocol versions.

//nolint:unused // Unused in this file, but exported for use in other files.
var (
	protoHTTP10 = []byte("HTTP/1.0")
	protoHTTP11 = []byte("HTTP/1.1")
	protoHTTP20 = []byte("HTTP/2.0")
)

// Common delimiters.

//nolint:unused // Unused in this file, but exported for use in other files.
var (
	crlf    = []byte("\r\n")
	colonSp = []byte(": ")
)

// NewFastParser creates a new high-performance HTTP parser.
func NewFastParser(conn net.Conn, engine *Engine, processor Processor, isClient bool, executor func(f func()) bool) ParserCloser {
	if processor == nil {
		processor = NewEmptyProcessor()
	}
	if executor == nil {
		executor = func(f func()) bool {
			f()
			return true
		}
	}

	allocator := mempool.DefaultMemPool
	if engine != nil && engine.BodyAllocator != nil {
		allocator = engine.BodyAllocator
	}

	return &FastParser{
		state:     fastStateMethodStart,
		headers:   make([][2][]byte, 0, 16),
		allocator: allocator,
		requestPool: sync.Pool{
			New: func() interface{} {
				return &http.Request{}
			},
		},
		conn:      conn,
		engine:    engine,
		processor: processor,
		execute:   executor,
	}
}

// UnderlayerConn returns the underlying connection.
func (p *FastParser) UnderlayerConn() net.Conn {
	return p.conn
}

// Reset resets the parser state.
func (p *FastParser) Reset() {
	p.state = fastStateMethodStart
	p.method = nil
	p.path = nil
	p.proto = nil
	p.headers = p.headers[:0]
	p.body = nil
	p.contentLength = 0
	p.keepAlive = false
	p.chunked = false
	p.chunkSize = 0
	p.chunkRead = 0
	p.complete = false
	p.err = nil
	p.startTime = time.Time{}
	p.parseTime = 0
}

// Parse parses HTTP request
//
//nolint:golint // Ignore the error return value of mempool.Append
func (p *FastParser) Parse(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	if p.startTime.IsZero() {
		p.startTime = time.Now()
	}

	var start = 0
	var offset = 0
	if p.bytesCached != nil {
		offset = len(*p.bytesCached)
	}
	if offset > 0 {
		if p.engine.ReadLimit > 0 && offset+len(data) > p.engine.ReadLimit {
			return ErrTooLong
		}
		p.bytesCached = mempool.Append(p.bytesCached, data...)
		data = *p.bytesCached
	}

	var i int
	for i = offset; i < len(data) && p.state != fastStateComplete && p.state != fastStateError; i++ {
		c := data[i]

		switch p.state {
		default:
			p.err = fmt.Errorf("invalid state: %d", p.state)
			p.state = fastStateError

		case fastStateComplete:
			// Request is complete, ignore remaining data
			continue

		case fastStateError:
			// Error state, ignore remaining data
			continue

		case fastStateHeaderValueEnd:
			// Skip CRLF after header value
			if c == '\r' {
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
				}
			} else if c == '\n' {
				p.state = fastStateHeaderStart
			} else {
				p.err = fmt.Errorf("expected CR or LF, got: %q", c)
				p.state = fastStateError
			}

		case fastStateBody:
			// Read body until end
			remaining := len(data) - i
			if remaining >= p.contentLength {
				// Have enough data to read the entire request body
				p.body = data[i : i+p.contentLength]
				p.processor.OnBody(nil, p.body)
				i += p.contentLength - 1 // -1是因为循环会自动加1
				p.state = fastStateComplete
			} else {
				// Not enough data, need more
				p.body = data[i:]
				p.processor.OnBody(nil, p.body)
				i = len(data) - 1 // 读取所有剩余数据
				_ = i
				return io.ErrUnexpectedEOF
			}

		case fastStateMethodStart:
			// Skip leading whitespace
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				continue
			}
			start = i
			p.state = fastStateMethod

		case fastStateMethod:
			// Read method until space
			if c == ' ' {
				p.method = data[start:i]
				p.processor.OnMethod(nil, string(p.method))
				p.state = fastStatePathStart
			} else if !isTokenChar(c) {
				p.err = fmt.Errorf("invalid method character: %q", c)
				p.state = fastStateError
			}

		case fastStatePathStart:
			// Skip spaces
			if c == ' ' {
				continue
			}
			start = i
			p.state = fastStatePath

		case fastStatePath:
			// Read path until space
			if c == ' ' {
				p.path = data[start:i]
				p.processor.OnURL(nil, string(p.path))
				p.state = fastStateProtoStart
			}

		case fastStateProtoStart:
			// Skip spaces
			if c == ' ' {
				continue
			}
			start = i
			p.state = fastStateProto

		case fastStateProto:
			// Read protocol until CRLF
			if c == '\r' {
				p.proto = data[start:i]
				p.processor.OnProto(nil, string(p.proto))
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
					p.state = fastStateHeaderStart
				} else {
					p.err = errors.New("expected LF after CR")
					p.state = fastStateError
				}
			}

		case fastStateHeaderStart:
			// Check if headers are finished
			if c == '\r' {
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
					// Process headers
					p.processHeaders()
					if p.contentLength > 0 {
						p.state = fastStateBodyStart
					} else if p.chunked {
						p.state = fastStateChunkSizeStart
					} else {
						p.state = fastStateComplete
					}
				} else {
					p.err = errors.New("expected LF after CR")
					p.state = fastStateError
				}
			} else if c == '\n' {
				// Process headers
				p.processHeaders()
				if p.contentLength > 0 {
					p.state = fastStateBodyStart
				} else if p.chunked {
					p.state = fastStateChunkSizeStart
				} else {
					p.state = fastStateComplete
				}
			} else if !isWhitespace(c) {
				start = i
				p.state = fastStateHeaderName
			}

		case fastStateHeaderName:
			// Read header name until colon
			if c == ':' {
				name := data[start:i]
				p.state = fastStateHeaderValueStart

				// Add new header
				p.headers = append(p.headers, [2][]byte{name, nil})
			} else if !isTokenChar(c) {
				p.err = fmt.Errorf("invalid header name character: %q", c)
				p.state = fastStateError
			}

		case fastStateHeaderValueStart:
			// Skip spaces after colon
			if c == ' ' || c == '\t' {
				continue
			}
			start = i
			p.state = fastStateHeaderValue

		case fastStateHeaderValue:
			// Read header value until CRLF
			if c == '\r' {
				value := data[start:i]
				p.headers[len(p.headers)-1][1] = value
				p.processor.OnHeader(nil, string(p.headers[len(p.headers)-1][0]), string(value))

				if i+1 < len(data) && data[i+1] == '\n' {
					i++
					p.state = fastStateHeaderStart
				} else {
					p.err = errors.New("expected LF after CR")
					p.state = fastStateError
				}
			} else if c == '\n' {
				value := data[start:i]
				p.headers[len(p.headers)-1][1] = value
				p.processor.OnHeader(nil, string(p.headers[len(p.headers)-1][0]), string(value))
				p.state = fastStateHeaderStart
			}

		case fastStateBodyStart:
			// Start reading request body
			if p.contentLength > 0 {
				remaining := len(data) - i
				if remaining >= p.contentLength {
					// Have enough data to read the entire request body
					p.body = data[i : i+p.contentLength]
					p.processor.OnBody(nil, p.body)
					i += p.contentLength - 1 // -1是因为循环会自动加1
					p.state = fastStateComplete
				} else {
					// Not enough data, need more
					p.body = data[i:]
					p.processor.OnBody(nil, p.body)
					i = len(data) - 1 // 读取所有剩余数据
					_ = i
					return io.ErrUnexpectedEOF
				}
			} else {
				p.state = fastStateComplete
			}

		case fastStateChunkSizeStart:
			// Skip leading whitespace
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				continue
			}
			start = i
			p.state = fastStateChunkSize

		case fastStateChunkSize:
			// Read chunk size until CRLF
			if c == '\r' || c == '\n' || c == ';' {
				sizeHex := string(data[start:i])
				size, err := strconv.ParseInt(sizeHex, 16, 32)
				if err != nil {
					p.err = fmt.Errorf("invalid chunk size: %s", sizeHex)
					p.state = fastStateError
				} else {
					p.chunkSize = int(size)
					p.chunkRead = 0

					if p.chunkSize == 0 {
						// Last chunk
						p.state = fastStateComplete
					} else {
						// Skip CRLF
						if c == '\r' {
							if i+1 < len(data) && data[i+1] == '\n' {
								i++
							}
						}
						p.state = fastStateChunkData
					}
				}
			} else if !isHexDigit(c) {
				p.err = fmt.Errorf("invalid character in chunk size: %q", c)
				p.state = fastStateError
			}

		case fastStateChunkData:
			// Read chunk data
			remaining := len(data) - i
			toRead := p.chunkSize - p.chunkRead

			if remaining >= toRead {
				// Have enough data to read the entire chunk
				chunk := data[i : i+toRead]
				p.processor.OnBody(nil, chunk)

				i += toRead - 1 // -1是因为循环会自动加1
				p.chunkRead = p.chunkSize
				p.state = fastStateChunkDataEnd
			} else {
				// Not enough data, need more
				chunk := data[i:]
				p.processor.OnBody(nil, chunk)

				p.chunkRead += remaining
				i = len(data) - 1 // 读取所有剩余数据
				_ = i
				return io.ErrUnexpectedEOF
			}

		case fastStateChunkDataEnd:
			// Skip CRLF after chunk data
			if c == '\r' {
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
					p.state = fastStateChunkSizeStart
				} else {
					p.err = errors.New("expected LF after CR")
					p.state = fastStateError
				}
			} else if c == '\n' {
				p.state = fastStateChunkSizeStart
			} else {
				p.err = fmt.Errorf("expected CR or LF, got: %q", c)
				p.state = fastStateError
			}
		}
	}

	if p.state == fastStateError {
		return p.err
	}

	if p.state == fastStateComplete {
		p.parseTime = time.Since(p.startTime)
		p.complete = true
		p.processor.OnComplete(nil)
		p.Reset()
		return nil
	}

	// Save unprocessed data
	left := len(data) - start
	if left > 0 {
		if p.bytesCached == nil {
			p.bytesCached = mempool.Malloc(left)
			copy(*p.bytesCached, data[start:])
		} else if start > 0 {
			oldbytesCached := p.bytesCached
			p.bytesCached = mempool.Malloc(left)
			copy(*p.bytesCached, data[start:])
			mempool.Free(oldbytesCached)
		}
	} else if p.bytesCached != nil && len(*p.bytesCached) > 0 {
		mempool.Free(p.bytesCached)
		p.bytesCached = nil
	}

	// Need more data
	return io.ErrUnexpectedEOF
}

// CloseAndClean closes the parser and cleans up resources.
func (p *FastParser) CloseAndClean(err error) {
	if p.bytesCached != nil {
		mempool.Free(p.bytesCached)
		p.bytesCached = nil
	}

	if p.processor != nil {
		p.processor.Close(nil, err)
	}

	if p.onClose != nil {
		p.onClose(nil, err)
	}
}

// processHeaders processes parsed headers.
func (p *FastParser) processHeaders() {
	for _, header := range p.headers {
		name := header[0]
		value := header[1]

		// Convert to lowercase for comparison
		nameLower := bytes.ToLower(name)

		if bytes.Equal(nameLower, bytes.ToLower(headerContentLength)) {
			// Parse Content-Length
			cl, err := strconv.Atoi(string(value))
			if err == nil && cl >= 0 {
				p.contentLength = cl
				p.processor.OnContentLength(nil, cl)
			}
		} else if bytes.Equal(nameLower, bytes.ToLower(headerTransferEncoding)) {
			// Check if using chunked transfer
			valueLower := bytes.ToLower(value)
			if bytes.Contains(valueLower, bytes.ToLower(valueChunked)) {
				p.chunked = true
			}
		} else if bytes.Equal(nameLower, bytes.ToLower(headerConnection)) {
			// Check if connection should be kept alive
			valueLower := bytes.ToLower(value)
			if bytes.Contains(valueLower, bytes.ToLower(valueClose)) {
				p.keepAlive = false
			} else if bytes.Contains(valueLower, bytes.ToLower(valueKeepAlive)) {
				p.keepAlive = true
			}
		}
	}

	// If HTTP/1.1, keep connection alive by default
	if bytes.Equal(p.proto, protoHTTP11) {
		p.keepAlive = true
	}
}

// isTokenChar checks if a character is a valid token character.
func isTokenChar(c byte) bool {
	return c > 31 && c < 127 && !isDelimiter(c)
}

// isDelimiter checks if a character is a delimiter.
func isDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=', '{', '}', ' ', '\t':
		return true
	}
	return false
}

// isWhitespace checks if a character is a whitespace character.
func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// isHexDigit checks if a character is a hexadecimal digit.
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// Register parser.
func init() {
	// Register FastParser as an optional HTTP parser
	RegisterParserType("fast", NewFastParser)
}

// ParserFactory is a factory function type for creating parsers.
type ParserFactory func(conn net.Conn, engine *Engine, processor Processor, isClient bool, executor func(f func()) bool) ParserCloser

// Global parser factory map.
var parserFactories = make(map[string]ParserFactory)

// RegisterParserType registers an HTTP parser type.
func RegisterParserType(name string, factory ParserFactory) {
	parserFactories[name] = factory
}

// GetParserType gets an HTTP parser type.
func GetParserType(name string) ParserFactory {
	if factory, ok := parserFactories[name]; ok {
		return factory
	}
	// Use standard parser by default
	return func(conn net.Conn, engine *Engine, processor Processor, isClient bool, executor func(f func()) bool) ParserCloser {
		return NewParser(conn, engine, processor, isClient, executor)
	}
}
