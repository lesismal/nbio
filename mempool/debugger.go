package mempool

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

type sizeMap struct {
	MallocCount int64 `json:"MallocCount"`
	FreeCount   int64 `json:"FreeCount"`
	NeedFree    int64 `json:"NeedFree"`
}

type debugger struct {
	mux         sync.Mutex
	on          bool
	MallocCount int64            `json:"MallocCount"`
	FreeCount   int64            `json:"FreeCount"`
	NeedFree    int64            `json:"NeedFree"`
	SizeMap     map[int]*sizeMap `json:"SizeMap"`
}

//go:norace
func (d *debugger) SetDebug(dbg bool) {
	d.on = dbg
}

//go:norace
func (d *debugger) incrMalloc(pbuf *[]byte) {
	if d.on {
		d.incrMallocSlow(pbuf)
	}
}

//go:norace
func (d *debugger) incrMallocSlow(pbuf *[]byte) {
	atomic.AddInt64(&d.MallocCount, 1)
	atomic.AddInt64(&d.NeedFree, 1)
	size := cap(*pbuf)
	d.mux.Lock()
	defer d.mux.Unlock()
	if d.SizeMap == nil {
		d.SizeMap = map[int]*sizeMap{}
	}
	if v, ok := d.SizeMap[size]; ok {
		v.MallocCount++
		v.NeedFree++
	} else {
		d.SizeMap[size] = &sizeMap{
			MallocCount: 1,
			NeedFree:    1,
		}
	}
}

//go:norace
func (d *debugger) incrFree(pbuf *[]byte) {
	if d.on {
		d.incrFreeSlow(pbuf)
	}
}

//go:norace
func (d *debugger) incrFreeSlow(pbuf *[]byte) {
	atomic.AddInt64(&d.FreeCount, 1)
	atomic.AddInt64(&d.NeedFree, -1)
	size := cap(*pbuf)
	d.mux.Lock()
	defer d.mux.Unlock()
	if v, ok := d.SizeMap[size]; ok {
		v.FreeCount++
		v.NeedFree--
	} else {
		d.SizeMap[size] = &sizeMap{
			MallocCount: 1,
			NeedFree:    -1,
		}
	}
}

//go:norace
func (d *debugger) String() string {
	if d.on {
		b, err := json.Marshal(d)
		if err == nil {
			return string(b)
		}
	}
	return ""
}
