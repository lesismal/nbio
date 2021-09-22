// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

const (
	// state: RequestLine
	stateClose int8 = iota
	stateMethodBefore
	stateMethod

	statePathBefore
	statePath
	stateProtoBefore
	stateProto
	stateProtoLF
	stateClientProtoBefore
	stateClientProto
	stateStatusCodeBefore
	stateStatusCode
	stateStatusBefore
	stateStatus
	stateStatusLF

	// state: Header
	stateHeaderKeyBefore
	stateHeaderValueLF
	stateHeaderKey

	stateHeaderValueBefore
	stateHeaderValue

	// state: Body ContentLength
	stateBodyContentLength

	// state: Body Chunk
	stateHeaderOverLF
	stateBodyChunkSizeBefore
	stateBodyChunkSize
	stateBodyChunkSizeLF
	stateBodyChunkData
	stateBodyChunkDataCR
	stateBodyChunkDataLF

	// state: Body Trailer
	stateBodyTrailerHeaderValueLF
	stateBodyTrailerHeaderKeyBefore
	stateBodyTrailerHeaderKey
	stateBodyTrailerHeaderValueBefore
	stateBodyTrailerHeaderValue

	// state: Body CRLF
	stateTailCR
	stateTailLF
)
