// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64
// +build amd64 arm64

package websocket

// maskXORSIMDAsm 是平台特定的SIMD实现
// 在不同平台上有不同的汇编实现
//
//go:noescape
func maskXORSIMDAsm(b []byte, key [4]byte, pos int) int
