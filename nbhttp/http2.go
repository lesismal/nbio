// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

// import (
// 	"encoding/binary"
// 	"fmt"
// 	"net"
// 	"net/http"
// 	"time"
// 	"unsafe"

// 	"github.com/lesismal/nbio/mempool"
// )

// const (
// 	H2HeaderLen = 9
// )

// // Http2Upgrader .
// type Http2Upgrader struct {
// 	smReaded bool

// 	ReadLimit        int64
// 	HandshakeTimeout time.Duration

// 	Subprotocols []string

// 	CheckOrigin func(r *http.Request) bool

// 	buffer  []byte
// 	message []byte
// }

// // Upgrade .
// func (u *Http2Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (net.Conn, error) {
// 	return nil, nil
// }

// // Read .
// func (u *Http2Upgrader) Read(p *Parser, data []byte) error {
// 	l := len(u.buffer)
// 	if u.ReadLimit > 0 && (int64(l+len(data)) > u.ReadLimit || int64(l+len(u.message)) > u.ReadLimit) {
// 		return ErrTooLong
// 	}

// 	if l > 0 {
// 		u.buffer = mempool.Realloc(u.buffer, l+len(data))
// 		copy(u.buffer[l:], data)
// 	} else {
// 		u.buffer = data
// 	}

// 	buffer := u.buffer
// 	for {
// 		if !u.smReaded {
// 			if len(buffer) >= 6 {
// 				b := buffer[:6]
// 				sm := *(*string)(unsafe.Pointer(&b))
// 				if sm != "SM\r\n\r\n" {
// 					return ErrInvalidH2SM
// 				}
// 				buffer = buffer[6:]
// 				u.smReaded = true
// 			}
// 		} else {
// 			if len(buffer) >= H2HeaderLen {
// 				payloadLen := binary.BigEndian.Uint32(buffer[:4]) >> 8
// 				typ := buffer[3]
// 				flags := buffer[4]
// 				streamID := binary.BigEndian.Uint32(buffer[5:9])
// 				r := streamID >> 31
// 				if r != 0 {
// 					return ErrInvalidH2HeaderR
// 				}
// 				streamID &= 0x7FFFFFFF
// 				buffer = buffer[H2HeaderLen:]

// 				var body []byte
// 				if len(buffer) >= int(payloadLen) {
// 					body = buffer[:payloadLen]
// 					buffer = buffer[payloadLen:]
// 				}

// 				fmt.Printf("h2 header recved, payloadLen: %v, type: %v, flags: %v, streamID: %v, r: %v, body: %v\n", payloadLen, typ, flags, streamID, r, body)
// 			} else {
// 				break
// 			}
// 		}
// 	}

// 	l = len(u.buffer)
// 	if l != len(buffer) {
// 		var tmp []byte
// 		if l > 0 {
// 			if l < 2048 {
// 				tmp = mempool.Malloc(2048)[:l]
// 			} else {
// 				tmp = mempool.Malloc(l)
// 			}
// 			copy(tmp, u.buffer)
// 		}
// 		if l > 0 {
// 			mempool.Free(buffer)
// 		}
// 		u.buffer = tmp
// 	}

// 	return nil
// }

// // Close .
// func (u *Http2Upgrader) Close(p *Parser, err error) {

// }
