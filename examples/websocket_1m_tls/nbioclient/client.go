package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/lesismal/nbio/taskpool"
)

var (
	connected    uint64 = 0
	success      uint64 = 0
	failed       uint64 = 0
	totalSuccess uint64 = 0
	totalFailed  uint64 = 0

	sleepTime = flag.Int("s", 1, "sleep time for each loop in a goroutine")
	numClient = flag.Int("c", 50000, "client num")

	text = "hello world"
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		if *sleepTime > 0 {
			time.AfterFunc(time.Second*time.Duration(*sleepTime), func() {
				err := c.WriteMessage(messageType, data)
				if err != nil {
					fmt.Println("WriteMessage failed 111:", err)
					atomic.AddUint64(&failed, 1)
					panic(err)
				}
			})
		} else {
			err := c.WriteMessage(messageType, data)
			if err != nil {
				fmt.Println("WriteMessage failed 111:", err)
				atomic.AddUint64(&failed, 1)
				panic(err)
			}
		}
		if string(data) != text {
			atomic.AddUint64(&failed, 1)
			panic(fmt.Errorf("not equal: %v != %v", string(data), text))
		} else {
			atomic.AddUint64(&success, 1)
		}
	})

	u.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})

	return u
}

func main() {
	flag.Parse()

	engine := nbhttp.NewEngine(nbhttp.Config{})
	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	connNum := *numClient
	wg := sync.WaitGroup{}
	conns := make([]*websocket.Conn, connNum)
	pool := taskpool.NewFixedNoOrderPool(8, 1024)

	go func() {
		for i := 0; i < connNum; i++ {
			addr := addrs[i%len(addrs)]
			u := url.URL{Scheme: "wss", Host: addr, Path: "/wss"}
			tlsConfig := &tls.Config{
				InsecureSkipVerify: true,
			}
			dialer := &websocket.Dialer{
				Engine:          engine,
				Upgrader:        newUpgrader(),
				DialTimeout:     time.Second * 3,
				TLSClientConfig: tlsConfig,
			}
			idx := i
			wg.Add(1)
			pool.Go(func() {
				defer wg.Done()
				for {
					conn, _, err := dialer.Dial(u.String(), nil)
					if err == nil {
						conns[idx] = conn
						atomic.AddUint64(&connected, 1)
						break
					}
					time.Sleep(time.Second / 10)
				}
			})
		}

		wg.Wait()

		for i := 0; i < connNum; i++ {
			c := conns[i]
			text := "hello world"
			err := c.WriteMessage(websocket.TextMessage, []byte(text))
			if err != nil {
				fmt.Println("WriteMessage failed 111:", err)
				atomic.AddUint64(&failed, 1)
				panic(err)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(time.Second)
		for i := 1; true; i++ {
			<-ticker.C
			nSuccess := atomic.SwapUint64(&success, 0)
			nFailed := atomic.SwapUint64(&failed, 0)
			totalSuccess += nSuccess
			totalFailed += nFailed
			fmt.Printf("running for %v seconds, online: %v, NumGoroutine: %v, success: %v, totalSuccess: %v, failed: %v, totalFailed: %v\n", i, connected, runtime.NumGoroutine(), nSuccess, totalSuccess, nFailed, totalFailed)
		}
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
}

var addrs = []string{
	"localhost:28001",
	"localhost:28002",
	"localhost:28003",
	"localhost:28004",
	"localhost:28005",
	"localhost:28006",
	"localhost:28007",
	"localhost:28008",
	"localhost:28009",
	"localhost:28010",

	"localhost:28011",
	"localhost:28012",
	"localhost:28013",
	"localhost:28014",
	"localhost:28015",
	"localhost:28016",
	"localhost:28017",
	"localhost:28018",
	"localhost:28019",
	"localhost:28020",

	"localhost:28021",
	"localhost:28022",
	"localhost:28023",
	"localhost:28024",
	"localhost:28025",
	"localhost:28026",
	"localhost:28027",
	"localhost:28028",
	"localhost:28029",
	"localhost:28030",

	"localhost:28031",
	"localhost:28032",
	"localhost:28033",
	"localhost:28034",
	"localhost:28035",
	"localhost:28036",
	"localhost:28037",
	"localhost:28038",
	"localhost:28039",
	"localhost:28040",

	"localhost:28041",
	"localhost:28042",
	"localhost:28043",
	"localhost:28044",
	"localhost:28045",
	"localhost:28046",
	"localhost:28047",
	"localhost:28048",
	"localhost:28049",
	"localhost:28050",
}
