// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"errors"
)

var (
	errReadTimeout  = errors.New("read timeout")
	errWriteTimeout = errors.New("write timeout")
)
