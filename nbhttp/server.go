// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"net/http"

	"github.com/lesismal/llib/std/crypto/tls"
)

type Server struct {
	*Engine
}

// NewServer .
func NewServer(conf Config, handler http.Handler, messageHandlerExecutor func(f func()), v ...interface{}) *Server {
	args := append([]interface{}{handler, messageHandlerExecutor}, v...)
	engine := NewEngine(conf, args...)
	return &Server{Engine: engine}
}

// NewServerTLS .
func NewServerTLS(conf Config, handler http.Handler, messageHandlerExecutor func(f func()), tlsConfig *tls.Config, v ...interface{}) *Server {
	args := append([]interface{}{handler, messageHandlerExecutor, tlsConfig}, v...)
	engine := NewEngineTLS(conf, args...)
	return &Server{Engine: engine}
}
