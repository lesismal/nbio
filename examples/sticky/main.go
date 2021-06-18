package main

import (
	"flag"

	"github.com/lesismal/nbio/examples/sticky/proxy"
)

var (
	src = flag.String("src", "localhost:8888", "src addr for listening")
	dst = flag.String("dst", "localhost:9999", "dst addr for dialing")
)

func main() {
	flag.Parse()
	proxy.Run(*src, *dst)
}
