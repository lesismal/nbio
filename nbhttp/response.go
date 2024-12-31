// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
)

// Response represents the server side of an HTTP response.
type Response struct {
	Parser *Parser

	request *http.Request // request for this response.

	status     string
	statusCode int // status code passed to WriteHeader.

	header      http.Header
	trailer     map[string]string
	trailerSize int

	buffer       *[]byte
	bodyBuffer   *[]byte
	contentLen   int
	bodyWritten  int
	intFormatBuf [10]byte

	chunked      bool
	chunkChecked bool
	headEncoded  bool
	hasBody      bool
	hijacked     bool
}

// Hijack .
//
//go:norace
func (res *Response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if res.Parser == nil {
		return nil, nil, errors.New("nil Proccessor")
	}
	res.hijacked = true
	return res.Parser.Conn, nil, nil
}

// Header .
//
//go:norace
func (res *Response) Header() http.Header {
	return res.header
}

// WriteHeader .
//
//go:norace
func (res *Response) WriteHeader(statusCode int) {
	if !res.hijacked && res.statusCode == 0 && res.statusCode != statusCode {
		status := http.StatusText(statusCode)
		if status != "" {
			res.status = status
			res.statusCode = statusCode
		}

		if cl := res.header.Get(contentLengthHeader); cl != "" {
			v, err := strconv.ParseInt(cl, 10, 64)
			if err == nil && v >= 0 {
			} else {
				logging.Error("http: invalid Content-Length of %q", cl)
				res.header.Del(contentLengthHeader)
			}
		}

		res.checkChunked()
	}
}

const maxPacketSize = 65536

// WriteString .
//
//go:norace
func (res *Response) WriteString(s string) (int, error) {
	x := (*[2]uintptr)(unsafe.Pointer(&s))
	h := [3]uintptr{x[0], x[1], x[1]}
	data := *(*[]byte)(unsafe.Pointer(&h))
	return res.Write(data)
}

// Write .
//
//go:norace
func (res *Response) Write(data []byte) (int, error) {
	l := len(data)
	conn := res.Parser.Conn
	if l == 0 || conn == nil {
		return 0, nil
	}

	res.WriteHeader(http.StatusOK)

	res.hasBody = true

	if res.chunked {
		res.eoncodeHead()

		pbuf := res.buffer
		hl := len(*pbuf)
		res.buffer = nil
		lenStr := res.formatInt(l, 16)
		size := hl + len(lenStr) + l + 4
		if size < maxPacketSize {
			pbuf = mempool.AppendString(pbuf, lenStr)
			pbuf = mempool.AppendString(pbuf, "\r\n")
			pbuf = mempool.Append(pbuf, data...)
			pbuf = mempool.AppendString(pbuf, "\r\n")
			res.buffer = pbuf
			return l, nil
		}
		pbuf = mempool.AppendString(pbuf, lenStr)
		pbuf = mempool.AppendString(pbuf, "\r\n")
		_, err := conn.Write(*pbuf)
		mempool.Free(pbuf)
		if err != nil {
			return 0, err
		}
		pbuf = mempool.Malloc(len(data) + 2)
		*pbuf = (*pbuf)[0:0]
		pbuf = mempool.Append(pbuf, data...)
		pbuf = mempool.AppendString(pbuf, "\r\n")
		if len(*pbuf) < maxPacketSize {
			res.buffer = pbuf
			return l, nil
		}
		_, err = conn.Write(*pbuf)
		mempool.Free(pbuf)
		if err != nil {
			return 0, err
		}
		return l, nil
	}

	cl, err := res.contentLength()
	if err != nil {
		return 0, err
	}
	if cl > 0 && res.bodyWritten+l > cl {
		return 0, http.ErrContentLength
	}
	if len(res.header[contentLengthHeader]) > 0 {
		res.eoncodeHead()

		pbuf := res.buffer
		res.buffer = nil
		if pbuf == nil {
			return conn.Write(data)
		}
		pbuf = mempool.Append(pbuf, data...)
		l, err := conn.Write(*pbuf)
		mempool.Free(pbuf)
		if l >= 0 {
			res.bodyWritten += l
		}
		return l, err
	}
	if res.bodyBuffer == nil {
		res.bodyBuffer = mempool.Malloc(l)
		*res.bodyBuffer = (*res.bodyBuffer)[0:0]
	}
	res.bodyBuffer = mempool.Append(res.bodyBuffer, data...)
	res.bodyWritten += l
	// res.header[contentLengthHeader] = []string{res.formatInt(l, 10)}
	return l, nil
}

func (res *Response) contentLength() (int, error) {
	if res.contentLen > 0 {
		return res.contentLen, nil
	}
	cl := res.header.Get(contentLengthHeader)
	if cl == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(cl, 10, 64)
	if err == nil {
		res.contentLen = int(v)
	}
	return int(v), err
}

// ReadFrom .
//
//go:norace
func (res *Response) ReadFrom(r io.Reader) (n int64, err error) {
	c := res.Parser.Conn
	if c == nil {
		return 0, nil
	}

	res.hasBody = true
	res.eoncodeHead()
	_, err = c.Write(*res.buffer)
	mempool.Free(res.buffer)
	res.buffer = nil
	if err != nil {
		return 0, err
	}

	if !res.Parser.Engine.DisableSendfile {
		lr, ok := r.(*io.LimitedReader)
		if ok {
			n, r = lr.N, lr.R
			if n <= 0 {
				return 0, nil
			}
		}

		f, ok := r.(*os.File)
		if ok {
			rc := c
			if hc, ok := c.(*Conn); ok {
				rc = hc.Conn
			}
			nc, ok := rc.(interface {
				Sendfile(f *os.File, remain int64) (int64, error)
			})
			if !ok {
				hc, ok2 := c.(*Conn)
				if ok2 {
					nc, ok = hc.Conn.(interface {
						Sendfile(f *os.File, remain int64) (int64, error)
					})
				}

			}
			if ok {
				ns, err := nc.Sendfile(f, lr.N)
				return ns, err
			}
		}
	}

	return io.Copy(c, r)
}

// checkChunked .
//
//go:norace
func (res *Response) checkChunked() {
	if res.chunkChecked {
		return
	}

	res.chunkChecked = true

	// res.WriteHeader(http.StatusOK)

	if res.request.ProtoAtLeast(1, 1) {
		for _, v := range res.header[transferEncodingHeader] {
			if v == "chunked" {
				res.chunked = true
			}
		}
		if !res.chunked {
			if len(res.header[trailerHeader]) > 0 {
				res.chunked = true
				hs := res.header[transferEncodingHeader]
				res.header[transferEncodingHeader] = append(hs, "chunked")
			}
		}
	}
	if res.chunked {
		delete(res.header, contentLengthHeader)
	}
}

// flush .
//
//go:norace
func (res *Response) eoncodeHead() {
	if res.headEncoded {
		return
	}

	res.WriteHeader(http.StatusOK)

	res.headEncoded = true

	status := res.status
	statusCode := res.statusCode

	pdata := mempool.Malloc(1024)
	*pdata = (*pdata)[0:0]

	pdata = mempool.AppendString(pdata, res.request.Proto)
	pdata = mempool.Append(pdata, ' ', '0'+byte(statusCode/100), '0'+byte(statusCode%100)/10, '0'+byte(statusCode%10), ' ')
	pdata = mempool.AppendString(pdata, status)
	pdata = mempool.Append(pdata, '\r', '\n')

	if res.hasBody && len(res.header["Content-Type"]) == 0 {
		const contentType = "Content-Type: text/plain; charset=utf-8\r\n"
		pdata = mempool.AppendString(pdata, contentType)
	}
	if !res.chunked && len(res.header[contentLengthHeader]) == 0 {
		const contentLenthPrefix = "Content-Length: "
		if !res.hasBody {
			pdata = mempool.AppendString(pdata, contentLenthPrefix)
			pdata = mempool.Append(pdata, '0', '\r', '\n')
		} else {
			pdata = mempool.AppendString(pdata, contentLenthPrefix)
			l := 0
			if res.bodyBuffer != nil {
				l = len(*res.bodyBuffer)
			}
			if l > 0 {
				s := strconv.FormatInt(int64(l), 10)
				pdata = mempool.AppendString(pdata, s)
				pdata = mempool.Append(pdata, '\r', '\n')
			} else {
				pdata = mempool.Append(pdata, '0', '\r', '\n')
			}
		}
	}
	if res.request.Close && len(res.header["Connection"]) == 0 {
		const connection = "Connection: close\r\n"
		pdata = mempool.AppendString(pdata, connection)
	}

	if len(res.header["Date"]) == 0 {
		const days = "SunMonTueWedThuFriSat"
		const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
		t := time.Now().UTC()
		yy, mm, dd := t.Date()
		hh, mn, ss := t.Clock()
		day := days[3*t.Weekday():]
		mon := months[3*(mm-1):]
		pdata = mempool.Append(pdata,
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
	}

	res.trailer = map[string]string{}
	trailers := res.header[trailerHeader]
	for _, k := range trailers {
		res.trailer[k] = ""
	}
	for k, vv := range res.header {
		if _, ok := res.trailer[k]; !ok {
			for _, v := range vv {
				pdata = mempool.AppendString(pdata, k)
				pdata = mempool.Append(pdata, ':', ' ')
				pdata = mempool.AppendString(pdata, v)
				pdata = mempool.Append(pdata, '\r', '\n')
			}
		} else if len(vv) > 0 {
			v := res.header.Get(k)
			res.trailer[k] = v
			res.trailerSize += (len(k) + len(v) + 4)
		}
	}

	pdata = mempool.Append(pdata, '\r', '\n')
	res.buffer = pdata
}

//go:norace
func (res *Response) flush(conn io.Writer) error {
	var err error

	if !res.chunked {
		if res.buffer != nil {
			if res.bodyBuffer != nil {
				res.buffer = mempool.Append(res.buffer, (*res.bodyBuffer)...)
				mempool.Free(res.bodyBuffer)
				res.bodyBuffer = nil
			}
			_, err = conn.Write(*res.buffer)
			mempool.Free(res.buffer)
			res.buffer = nil
			if err != nil {
				return err
			}
		}
		if res.bodyBuffer != nil {
			_, err = conn.Write(*res.bodyBuffer)
			mempool.Free(res.bodyBuffer)
			res.bodyBuffer = nil
		}

		return err
	}

	pdata := res.buffer
	res.buffer = nil
	if len(res.trailer) == 0 {
		if pdata == nil {
			pdata = mempool.Malloc(0)
		}
		pdata = mempool.AppendString(pdata, "0\r\n\r\n")
	} else {
		if pdata == nil {
			pdata = mempool.Malloc(512)
			*pdata = (*pdata)[0:0]
		}
		pdata = mempool.AppendString(pdata, "0\r\n")
		for k, v := range res.trailer {
			pdata = mempool.AppendString(pdata, k)
			pdata = mempool.AppendString(pdata, ": ")
			pdata = mempool.AppendString(pdata, v)
			pdata = mempool.AppendString(pdata, "\r\n")
		}
		pdata = mempool.AppendString(pdata, "\r\n")
	}
	_, err = conn.Write(*pdata)
	mempool.Free(pdata)
	return err
}

var numMap = []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

//go:norace
func (res *Response) formatInt(n int, base int) string {
	if n < 0 || n > 0x7FFFFFFF {
		return ""
	}

	buf := res.intFormatBuf[:]
	i := len(buf)
	for {
		i--
		buf[i] = numMap[n%base]
		n /= base
		if n <= 0 {
			break
		}
	}
	return string(buf[i:])
}

// NewResponse .
//
//go:norace
func NewResponse(parser *Parser, request *http.Request) *Response {
	res := responsePool.Get().(*Response)
	res.Parser = parser
	res.request = request
	res.header = http.Header{ /*"Server": []string{"nbio"}*/ }
	return res
}
