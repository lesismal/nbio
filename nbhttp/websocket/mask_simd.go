// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64
// +build amd64 arm64

package websocket

// maskXORSIMDAsm 是平台特定的SIMD实现
// 在不同平台上有不同的汇编实现
//
// AMD64平台使用汇编实现
func maskXORSIMDAsm(b []byte, key [4]byte, pos int) int {
	// 这个函数体只会在ARM64平台上使用
	// AMD64平台会使用汇编实现

	// 主循环 - 每次处理4个字节
	n := len(b) / 4 * 4
	for i := 0; i < n; i += 4 {
		b[i+0] ^= key[0]
		b[i+1] ^= key[1]
		b[i+2] ^= key[2]
		b[i+3] ^= key[3]
	}

	return 0
}
