// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"net/http"

	"github.com/lesismal/llib/std/crypto/tls"
)

// Server .
type Server struct {
	*Engine
}

// NewServer .
//
//go:norace
func NewServer(conf Config, v ...interface{}) *Server {
	if len(v) > 0 {
		if handler, ok := v[0].(http.Handler); ok {
			conf.Handler = handler
		}
	}
	if len(v) > 1 {
		if messageHandlerExecutor, ok := v[1].(func(f func())); ok {
			conf.ServerExecutor = messageHandlerExecutor
		}
	}
	return &Server{Engine: NewEngine(conf)}
}

// NewServerTLS .
//
//go:norace
func NewServerTLS(conf Config, v ...interface{}) *Server {
	if len(v) > 0 {
		if handler, ok := v[0].(http.Handler); ok {
			conf.Handler = handler
		}
	}
	if len(v) > 1 {
		if messageHandlerExecutor, ok := v[1].(func(f func())); ok {
			conf.ServerExecutor = messageHandlerExecutor
		}
	}
	if len(v) > 2 {
		if tlsConfig, ok := v[2].(*tls.Config); ok {
			conf.TLSConfig = tlsConfig
		}
	}
	conf.AddrsTLS = append(conf.AddrsTLS, conf.Addrs...)
	conf.Addrs = nil
	return &Server{Engine: NewEngine(conf)}
}
