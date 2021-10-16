package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/nbhttp"
)

var (
	success uint64 = 0
	failed  uint64 = 0

	sleepTime = flag.Int("s", 1, "sleep time for each loop in a goroutine")
)

func main() {
	flag.Parse()

	engine := nbhttp.NewEngine(nbhttp.Config{})

	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer engine.Stop()

	cli := &nbhttp.Client{
		Engine:          engine,
		Timeout:         time.Second * 3,
		MaxConnsPerHost: 5,
	}

	for i := 0; i < 100; i++ {
		idx := i
		go func() {
			count := 0

			func() {
				var doRequest func()
				doRequest = func() {
					var err error
					var req *http.Request
					req, err = http.NewRequest("GET", "http://localhost:8888/echo", nil)
					if err != nil {
						log.Fatal(err)
					}

					begin := time.Now()
					cli.Do(req, func(res *http.Response, conn net.Conn, err error) {
						if err != nil {
							atomic.AddUint64(&failed, 1)
							fmt.Println("Do failed:", err)
							return
						}
						atomic.AddUint64(&success, 1)
						if err == nil && res.Body != nil {
							defer res.Body.Close()
						}
						count++

						if *sleepTime > 0 {
							log.Printf("request success %v: %v, %v%v, time used: %v us\n", idx, count, req.URL.Host, req.URL.Path, time.Since(begin).Microseconds())
							time.AfterFunc(time.Second*time.Duration(*sleepTime), func() {
								doRequest()
							})
						} else {
							doRequest()
						}
					})
				}
				doRequest()
			}()
		}()
	}

	<-make(chan int)
}
