// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package websocket

import (
	"errors"
	"fmt"
)

var (
	// ErrUpgradeTokenNotFound .
	ErrUpgradeTokenNotFound = errors.New("websocket: the client is not using the websocket protocol: 'upgrade' token not found in 'Connection' header")

	// ErrUpgradeMethodIsGet .
	ErrUpgradeMethodIsGet = errors.New("websocket: the client is not using the websocket protocol: request method is not GET")

	// ErrUpgradeInvalidWebsocketVersion .
	ErrUpgradeInvalidWebsocketVersion = errors.New("websocket: unsupported version: 13 not found in 'Sec-Websocket-Version' header")

	// ErrUpgradeUnsupportedExtensions .
	ErrUpgradeUnsupportedExtensions = errors.New("websocket: application specific 'Sec-WebSocket-Extensions' headers are unsupported")

	// ErrUpgradeOriginNotAllowed .
	ErrUpgradeOriginNotAllowed = errors.New("websocket: request origin not allowed by Upgrader.CheckOrigin")

	// ErrUpgradeMissingWebsocketKey .
	ErrUpgradeMissingWebsocketKey = errors.New("websocket: not a websocket handshake: 'Sec-WebSocket-Key' header is missing or blank")

	// ErrUpgradeNotHijacker .
	ErrUpgradeNotHijacker = errors.New("websocket: response does not implement http.Hijacker")

	// ErrInvalidControlFrame .
	ErrInvalidControlFrame = errors.New("websocket: invalid control frame")

	// ErrInvalidWriteCalling .
	ErrInvalidWriteCalling = errors.New("websocket: invalid write calling, should call WriteMessage instead")

	// ErrReserveBitSet .
	ErrReserveBitSet = errors.New("websocket: reserved bit set it frame")

	// ErrReservedMessageType .
	ErrReservedMessageType = errors.New("websocket: reserved message type received")

	// ErrControlMessageFragmented .
	ErrControlMessageFragmented = errors.New("websocket: control messages must not be fragmented")

	// ErrControlMessageTooBig .
	ErrControlMessageTooBig = errors.New("websocket: control frame length > 125")

	// ErrFragmentsShouldNotHaveBinaryOrTextMessage .
	ErrFragmentsShouldNotHaveBinaryOrTextMessage = errors.New("websocket: fragments should not have message type of text or binary")

	// ErrInvalidCloseCode .
	ErrInvalidCloseCode = errors.New("websocket: invalid close code")

	// ErrBadHandshake .
	ErrBadHandshake = errors.New("websocket: bad handshake")

	// ErrInvalidCompression .
	ErrInvalidCompression = errors.New("websocket: invalid compression negotiation")

	// ErrInvalidUtf8 .
	ErrInvalidUtf8 = errors.New("websocket: invalid UTF-8 bytes")

	// ErrInvalidFragmentMessage .
	ErrInvalidFragmentMessage = errors.New("invalid fragment message")

	// ErrMalformedURL .
	ErrMalformedURL = errors.New("websocket: malformed ws or wss URL")

	// ErrMessageTooLarge.
	ErrMessageTooLarge = errors.New("message exceeds the configured limit")

	// ErrMessageSendQuqueIsFull .
	ErrMessageSendQuqueIsFull = errors.New("message send queue is full")
)

// CloseError .
type CloseError struct {
	Code   int
	Reason string
}

// Error .
//
//go:norace
func (ce CloseError) Error() string {
	return fmt.Sprintf("websocket: close code=%d and reason=%q", ce.Code, ce.Reason)
}

// CloseCode .
//
//go:norace
func CloseCode(err error) int {
	var ce CloseError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return -1
}

// CloseReason .
//
//go:norace
func CloseReason(err error) string {
	var ce CloseError
	if errors.As(err, &ce) {
		return ce.Reason
	}
	return ""
}
