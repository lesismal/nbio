package websocket

import (
	"errors"
)

var (
	ErrUpgradeTokenNotFound = errors.New("websocket: the client is not using the websocket protocol: 'upgrade' token not found in 'Connection' header")

	ErrUpgradeMethodIsGet = errors.New("websocket: the client is not using the websocket protocol: request method is not GET")

	ErrUpgradeInvalidWebsocketVersion = errors.New("websocket: unsupported version: 13 not found in 'Sec-Websocket-Version' header")

	ErrUpgradeUnsupportedExtensions = errors.New("websocket: application specific 'Sec-WebSocket-Extensions' headers are unsupported")

	ErrUpgradeOriginNotAllowed = errors.New("websocket: request origin not allowed by Upgrader.CheckOrigin")

	ErrUpgradeMissingWebsocketKey = errors.New("websocket: not a websocket handshake: 'Sec-WebSocket-Key' header is missing or blank")

	ErrUpgradeNotHijacker = errors.New("websocket: response does not implement http.Hijacker")

	ErrInvalidControlFrame = errors.New("websocket: invalid control frame")

	ErrInvalidWriteCalling = errors.New("websocket: invalid write calling, should call WriteMessage instead")
)
