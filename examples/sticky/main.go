package main

import (
	"flag"
	"time"

	"github.com/lesismal/nbio/examples/sticky/proxy"
)

var (
	src      = flag.String("src", "localhost:8888", "src addr for listening")
	dst      = flag.String("dst", "localhost:9999", "dst addr for dialing")
	interval = flag.Int("interval", 0, "byte sending interval, in nanoseconds")
)

func main() {
	flag.Parse()
	proxy.Run(*src, *dst, time.Duration(*interval), func(max int) int {
		return 1
	})
}
