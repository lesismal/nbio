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
		NPoller: runtime.NumCPU(),
	}, nil, nil, &tls.Config{}, clientExecutePool.Go)
	engine.InitTLSBuffers()

	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer engine.Stop()

	chWaitHttps := make(chan struct{})
	for i := 0; i < 2; i++ {
		idx := i
		go func() {
			count := 0
			cli := nbhttp.NewClient(engine)
			func() {
				var doRequest func(int)
				doRequest = func(cnt int) {
					var err error
					var req *http.Request
					if idx%2 == 0 {
						<-chWaitHttps
						req, err = http.NewRequest("GET", "http://localhost:8888/echo", nil)
					} else {
						defer func() {
							old := chWaitHttps
							chWaitHttps = make(chan struct{})
							close(old)
						}()
						req, err = http.NewRequest("GET", "https://github.com/lesismal", nil)
					}
					if err != nil {
						log.Fatal(err)
					}
					cli.Do(req, nil, func(res *http.Response, conn net.Conn, err error) {
						if err != nil {
							atomic.AddUint64(&failed, 1)
							fmt.Println("Do failed:", err)
							return
						} else {
							atomic.AddUint64(&success, 1)
							if err == nil && res.Body != nil {
								defer res.Body.Close()
								// body, err := io.ReadAll(res.Body)
								// if err == nil {
								// 	fmt.Println(string(body))
								// }
							}
							fmt.Printf("request success %v: %v%v\n", count, req.URL.Host, req.URL.Path)

							time.AfterFunc(time.Second, func() {
								count++
								doRequest(count)
							})
						}
					})
				}
				doRequest(count)
			}()
		}()
	}

	<-make(chan int)
}
