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

// FastParserState 表示FastParser的解析状态
type FastParserState int

// FastParser状态常量
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

// FastParser 是一个高性能的HTTP解析器
// 它使用状态机和零拷贝技术来提高解析效率
type FastParser struct {
	// 解析状态
	state FastParserState
	// 请求方法
	method []byte
	// 请求路径
	path []byte
	// 请求协议
	proto []byte
	// 请求头
	headers [][2][]byte
	// 请求体
	body []byte
	// 内容长度
	contentLength int
	// 是否保持连接
	keepAlive bool
	// 是否分块传输
	chunked bool
	// 当前块大小
	chunkSize int
	// 当前块已读取大小
	chunkRead int
	// 是否读取完成
	complete bool
	// 错误信息
	err error
	// 解析开始时间
	startTime time.Time
	// 解析耗时
	parseTime time.Duration
	// 内存分配器
	allocator mempool.Allocator
	// 请求对象池
	requestPool sync.Pool

	// ParserCloser接口所需字段
	conn      net.Conn
	engine    *Engine
	processor Processor
	execute   func(f func()) bool

	// 缓存的字节
	bytesCached *[]byte

	// 关闭回调
	onClose func(p *Parser, err error)
}

// 常用HTTP方法
var (
	methodGet     = []byte("GET")
	methodPost    = []byte("POST")
	methodPut     = []byte("PUT")
	methodDelete  = []byte("DELETE")
	methodHead    = []byte("HEAD")
	methodOptions = []byte("OPTIONS")
	methodPatch   = []byte("PATCH")
)

// 常用HTTP头
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

// 常用HTTP头值
var (
	valueChunked   = []byte("chunked")
	valueClose     = []byte("close")
	valueKeepAlive = []byte("keep-alive")
)

// 常用HTTP协议版本
var (
	protoHTTP10 = []byte("HTTP/1.0")
	protoHTTP11 = []byte("HTTP/1.1")
	protoHTTP20 = []byte("HTTP/2.0")
)

// 常用分隔符
var (
	crlf    = []byte("\r\n")
	colonSp = []byte(": ")
)

// NewFastParser 创建一个新的高性能HTTP解析器
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

// UnderlayerConn 返回底层连接
func (p *FastParser) UnderlayerConn() net.Conn {
	return p.conn
}

// Reset 重置解析器状态
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

// Parse 解析HTTP请求
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
		case fastStateMethodStart:
			// 跳过前导空白
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				continue
			}
			start = i
			p.state = fastStateMethod

		case fastStateMethod:
			// 读取方法直到空格
			if c == ' ' {
				p.method = data[start:i]
				p.processor.OnMethod(nil, string(p.method))
				p.state = fastStatePathStart
			} else if !isTokenChar(c) {
				p.err = fmt.Errorf("invalid method character: %q", c)
				p.state = fastStateError
			}

		case fastStatePathStart:
			// 跳过空格
			if c == ' ' {
				continue
			}
			start = i
			p.state = fastStatePath

		case fastStatePath:
			// 读取路径直到空格
			if c == ' ' {
				p.path = data[start:i]
				p.processor.OnURL(nil, string(p.path))
				p.state = fastStateProtoStart
			}

		case fastStateProtoStart:
			// 跳过空格
			if c == ' ' {
				continue
			}
			start = i
			p.state = fastStateProto

		case fastStateProto:
			// 读取协议直到CRLF
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
			// 检查是否是头部结束
			if c == '\r' {
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
					// 处理头部信息
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
				// 处理头部信息
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
			// 读取头部名称直到冒号
			if c == ':' {
				name := data[start:i]
				p.state = fastStateHeaderValueStart

				// 添加新的头部
				p.headers = append(p.headers, [2][]byte{name, nil})
			} else if !isTokenChar(c) {
				p.err = fmt.Errorf("invalid header name character: %q", c)
				p.state = fastStateError
			}

		case fastStateHeaderValueStart:
			// 跳过冒号后的空格
			if c == ' ' || c == '\t' {
				continue
			}
			start = i
			p.state = fastStateHeaderValue

		case fastStateHeaderValue:
			// 读取头部值直到CRLF
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
			// 开始读取请求体
			if p.contentLength > 0 {
				remaining := len(data) - i
				if remaining >= p.contentLength {
					// 有足够的数据读取整个请求体
					p.body = data[i : i+p.contentLength]
					p.processor.OnBody(nil, p.body)
					i += p.contentLength - 1 // -1是因为循环会自动加1
					p.state = fastStateComplete
				} else {
					// 数据不足，需要更多数据
					p.body = data[i:]
					p.processor.OnBody(nil, p.body)
					i = len(data) - 1 // 读取所有剩余数据
					return io.ErrUnexpectedEOF
				}
			} else {
				p.state = fastStateComplete
			}

		case fastStateChunkSizeStart:
			// 跳过前导空白
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				continue
			}
			start = i
			p.state = fastStateChunkSize

		case fastStateChunkSize:
			// 读取块大小直到CRLF
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
						// 最后一个块
						p.state = fastStateComplete
					} else {
						// 跳过CRLF
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
			// 读取块数据
			remaining := len(data) - i
			toRead := p.chunkSize - p.chunkRead

			if remaining >= toRead {
				// 有足够的数据读取整个块
				chunk := data[i : i+toRead]
				p.processor.OnBody(nil, chunk)

				i += toRead - 1 // -1是因为循环会自动加1
				p.chunkRead = p.chunkSize
				p.state = fastStateChunkDataEnd
			} else {
				// 数据不足，需要更多数据
				chunk := data[i:]
				p.processor.OnBody(nil, chunk)

				p.chunkRead += remaining
				i = len(data) - 1 // 读取所有剩余数据
				return io.ErrUnexpectedEOF
			}

		case fastStateChunkDataEnd:
			// 跳过块数据后的CRLF
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

	// 保存未处理的数据
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

	// 需要更多数据
	return io.ErrUnexpectedEOF
}

// CloseAndClean 关闭解析器并清理资源
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

// processHeaders 处理解析到的头部信息
func (p *FastParser) processHeaders() {
	for _, header := range p.headers {
		name := header[0]
		value := header[1]

		// 转换为小写进行比较
		nameLower := bytes.ToLower(name)

		if bytes.Equal(nameLower, bytes.ToLower(headerContentLength)) {
			// 解析Content-Length
			cl, err := strconv.Atoi(string(value))
			if err == nil && cl >= 0 {
				p.contentLength = cl
				p.processor.OnContentLength(nil, cl)
			}
		} else if bytes.Equal(nameLower, bytes.ToLower(headerTransferEncoding)) {
			// 检查是否分块传输
			valueLower := bytes.ToLower(value)
			if bytes.Contains(valueLower, bytes.ToLower(valueChunked)) {
				p.chunked = true
			}
		} else if bytes.Equal(nameLower, bytes.ToLower(headerConnection)) {
			// 检查连接是否保持
			valueLower := bytes.ToLower(value)
			if bytes.Contains(valueLower, bytes.ToLower(valueClose)) {
				p.keepAlive = false
			} else if bytes.Contains(valueLower, bytes.ToLower(valueKeepAlive)) {
				p.keepAlive = true
			}
		}
	}

	// 如果是HTTP/1.1，默认保持连接
	if bytes.Equal(p.proto, protoHTTP11) {
		p.keepAlive = true
	}
}

// isTokenChar 检查字符是否是有效的令牌字符
func isTokenChar(c byte) bool {
	return c > 31 && c < 127 && !isDelimiter(c)
}

// isDelimiter 检查字符是否是分隔符
func isDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=', '{', '}', ' ', '\t':
		return true
	}
	return false
}

// isWhitespace 检查字符是否是空白字符
func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// isHexDigit 检查字符是否是十六进制数字
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// 注册解析器
func init() {
	// 注册FastParser作为可选的HTTP解析器
	RegisterParserType("fast", NewFastParser)
}

// ParserFactory 是一个创建解析器的工厂函数类型
type ParserFactory func(conn net.Conn, engine *Engine, processor Processor, isClient bool, executor func(f func()) bool) ParserCloser

// 全局解析器工厂映射
var parserFactories = make(map[string]ParserFactory)

// RegisterParserType 注册HTTP解析器类型
func RegisterParserType(name string, factory ParserFactory) {
	parserFactories[name] = factory
}

// GetParserType 获取HTTP解析器类型
func GetParserType(name string) ParserFactory {
	if factory, ok := parserFactories[name]; ok {
		return factory
	}
	// 默认使用标准解析器
	return func(conn net.Conn, engine *Engine, processor Processor, isClient bool, executor func(f func()) bool) ParserCloser {
		return NewParser(conn, engine, processor, isClient, executor)
	}
}
