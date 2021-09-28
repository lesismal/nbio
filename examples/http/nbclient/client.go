package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/taskpool"
)

var (
	success uint64 = 0
	failed  uint64 = 0
)

func main() {
	flag.Parse()

	clientExecutePool := taskpool.NewMixedPool(1024, 1, 1024)
	engine := nbhttp.NewEngineTLS(nbhttp.Config{
		NPoller:       runtime.NumCPU(),
		SupportClient: true,
	}, nil, nil, &tls.Config{}, clientExecutePool.Go)

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

	for i := 0; i < 10; i++ {
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
						log.Printf("request success %v: %v, %v%v, time used: %v us\n", idx, count, req.URL.Host, req.URL.Path, time.Since(begin).Microseconds())
						time.AfterFunc(time.Second, func() {
							doRequest()
						})
					})
				}
				doRequest()
			}()
		}()
	}

	<-make(chan int)
}
