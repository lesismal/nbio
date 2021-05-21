// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

// FixedNoOrderPool .
type FixedNoOrderPool struct {
	chTask chan func()
}

func (np *FixedNoOrderPool) taskLoop() {
	for f := range np.chTask {
		call(f)
	}
}

// Go .
func (np *FixedNoOrderPool) Go(f func()) {
	np.chTask <- f
}

// GoByIndex .
func (np *FixedNoOrderPool) GoByIndex(index int, f func()) {
	np.Go(f)
}

// Go .
func (np *FixedNoOrderPool) Stop() {
	close(np.chTask)
}

// NewFixedNoOrderPool .
func NewFixedNoOrderPool(size int, bufferSize int) *FixedNoOrderPool {
	np := &FixedNoOrderPool{
		chTask: make(chan func(), bufferSize),
	}

	for i := 0; i < size; i++ {
		go np.taskLoop()
	}

	return np
}
