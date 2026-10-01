package nbio

import (
	"testing"
	"time"
)

// Stop called right after Start must return. Each poller's loop used to reset
// p.shutdown to false when its goroutine began, so a Stop that ran before the
// goroutine was scheduled was undone: the loop never exited and Stop waited
// on it forever.
func TestStopRightAfterStart(t *testing.T) {
	for i := 0; i < 100; i++ {
		g := NewEngine(Config{
			Network: "tcp",
			Addrs:   []string{"127.0.0.1:0"},
		})
		if err := g.Start(); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		done := make(chan struct{})
		go func() {
			g.Stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Stop did not return after Start (iteration %d)", i)
		}
	}
}
