package nbhttp

import (
	"runtime"
	"testing"
)

func TestDefaultNPoller(t *testing.T) {
	old := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(old)

	for _, tt := range []struct{ procs, want int }{
		{1, 1},
		{3, 1},
		{4, 1},
		{8, 2},
		{16, 4},
	} {
		runtime.GOMAXPROCS(tt.procs)
		if got := defaultNPoller(); got != tt.want {
			t.Errorf("GOMAXPROCS=%d: defaultNPoller() = %d, want %d", tt.procs, got, tt.want)
		}
	}
}

func TestNewEngineSizesFromGOMAXPROCS(t *testing.T) {
	old := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(old)
	runtime.GOMAXPROCS(8)

	e := NewEngine(Config{SupportServerOnly: true})
	if e.NPoller != 2 {
		t.Errorf("NPoller = %d, want 2", e.NPoller)
	}
	if e.MessageHandlerPoolSize != 8*1024 {
		t.Errorf("MessageHandlerPoolSize = %d, want %d", e.MessageHandlerPoolSize, 8*1024)
	}
}
