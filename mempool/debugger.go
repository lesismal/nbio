package mempool

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

type debugger struct {
	mux         sync.Mutex
	on          bool
	MallocCount int64
	FreeCount   int64
	NeedFree    int64
	SizeMap     map[int]*struct {
		MallocCount int64
		FreeCount   int64
		NeedFree    int64
	}
}

func (d *debugger) SetDebug(dbg bool) {
	d.on = dbg
}

func (d *debugger) incrMalloc(b []byte) {
	if d.on {
		d.incrMallocSlow(b)
	}
}
func (d *debugger) incrMallocSlow(b []byte) {
	atomic.AddInt64(&d.MallocCount, 1)
	atomic.AddInt64(&d.NeedFree, 1)
	size := cap(b)
	d.mux.Lock()
	defer d.mux.Unlock()
	if d.SizeMap == nil {
		d.SizeMap = map[int]*struct {
			MallocCount int64
			FreeCount   int64
			NeedFree    int64
		}{}
	}
	if v, ok := d.SizeMap[size]; ok {
		v.MallocCount++
		v.NeedFree++
	} else {
		d.SizeMap[size] = &struct {
			MallocCount int64
			FreeCount   int64
			NeedFree    int64
		}{
			MallocCount: 1,
			NeedFree:    1,
		}
	}
}

func (d *debugger) incrFree(b []byte) {
	if d.on {
		d.incrFreeSlow(b)
	}
}

func (d *debugger) incrFreeSlow(b []byte) {
	atomic.AddInt64(&d.FreeCount, 1)
	atomic.AddInt64(&d.NeedFree, -1)
	size := cap(b)
	d.mux.Lock()
	defer d.mux.Unlock()
	if v, ok := d.SizeMap[size]; ok {
		v.FreeCount++
		v.NeedFree--
	} else {
		d.SizeMap[size] = &struct {
			MallocCount int64
			FreeCount   int64
			NeedFree    int64
		}{
			MallocCount: 1,
			NeedFree:    -1,
		}
	}
}

func (d *debugger) String() string {
	if d.on {
		b, err := json.Marshal(d)
		if err == nil {
			return string(b)
		}
	}
	return ""
}
