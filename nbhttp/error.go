// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"errors"
)

var (
	// ErrInvalidCRLF .
	ErrInvalidCRLF = errors.New("invalid cr/lf at the end of line")

	// ErrInvalidHTTPVersion .
	ErrInvalidHTTPVersion = errors.New("invalid HTTP version")

	// ErrInvalidHTTPStatusCode .
	ErrInvalidHTTPStatusCode = errors.New("invalid HTTP status code")
	// ErrInvalidHTTPStatus .
	ErrInvalidHTTPStatus = errors.New("invalid HTTP status")

	// ErrInvalidMethod .
	ErrInvalidMethod = errors.New("invalid HTTP method")

	// ErrInvalidRequestURI .
	ErrInvalidRequestURI = errors.New("invalid URL")

	// ErrInvalidHost .
	ErrInvalidHost = errors.New("invalid host")

	// ErrInvalidPort .
	ErrInvalidPort = errors.New("invalid port")

	// ErrInvalidPath .
	ErrInvalidPath = errors.New("invalid path")

	// ErrInvalidQueryString .
	ErrInvalidQueryString = errors.New("invalid query string")

	// ErrInvalidFragment .
	ErrInvalidFragment = errors.New("invalid fragment")

	// ErrCRExpected .
	ErrCRExpected = errors.New("CR character expected")

	// ErrLFExpected .
	ErrLFExpected = errors.New("LF character expected")

	// ErrInvalidCharInHeader .
	ErrInvalidCharInHeader = errors.New("invalid character in header")

	// ErrUnexpectedContentLength .
	ErrUnexpectedContentLength = errors.New("unexpected content-length header")

	// ErrInvalidContentLength .
	ErrInvalidContentLength = errors.New("invalid ContentLength")

	// ErrInvalidChunkSize .
	ErrInvalidChunkSize = errors.New("invalid chunk size")

	// ErrTrailerExpected .
	ErrTrailerExpected = errors.New("trailer expected")

	// ErrTooLong .
	ErrTooLong = errors.New("invalid http message: too long")
)

var (
	// ErrInvalidH2SM .
	ErrInvalidH2SM = errors.New("invalid http2 SM characters")

	// ErrInvalidH2HeaderR .
	ErrInvalidH2HeaderR = errors.New("invalid http2 SM characters")
)

var (
	// ErrNilConn .
	ErrNilConn = errors.New("nil Conn")
)

var (
	// ErrClientUnsupportedSchema .
	ErrClientUnsupportedSchema = errors.New("unsupported schema")

	// ErrClientTimeout .
	ErrClientTimeout = errors.New("timeout")

	// ErrClientClosed .
	ErrClientClosed = errors.New("http client closed")
)

var (
	// ErrServiceOverload .
	ErrServiceOverload = errors.New("service overload")
)
