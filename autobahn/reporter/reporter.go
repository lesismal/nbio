package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

var (
	verbose = flag.Bool("verbose", false, "be verbose")
	web     = flag.String("http", "", "open web browser instead")
)

const (
	statusOK            = "OK"
	statusInformational = "INFORMATIONAL"
	statusUnimplemented = "UNIMPLEMENTED"
	statusNonStrict     = "NON-STRICT"
	statusUnclean       = "UNCLEAN"
	statusFailed        = "FAILED"
)

//go:norace
func failing(behavior string) bool {
	switch behavior {
	// case statusUnclean, statusFailed, statusNonStrict: // we should probably fix the nonstrict as well at some point
	case statusUnclean, statusFailed:
		return true
	default:
		return false
	}
}

type statusCounter struct {
	Total         int
	OK            int
	Informational int
	Unimplemented int
	NonStrict     int
	Unclean       int
	Failed        int
}

//go:norace
func (c *statusCounter) Inc(s string) {
	c.Total++
	switch s {
	case statusOK:
		c.OK++
	case statusInformational:
		c.Informational++
	case statusNonStrict:
		c.NonStrict++
	case statusUnimplemented:
		c.Unimplemented++
	case statusUnclean:
		c.Unclean++
	case statusFailed:
		c.Failed++
	default:
		panic(fmt.Sprintf("unexpected status %q", s))
	}
}

//go:norace
func main() {
	log.SetFlags(0)
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatalf("Usage: %s [options] <report-path>", os.Args[0])
	}

	base := path.Dir(flag.Arg(0))

	if addr := *web; addr != "" {
		http.HandleFunc("/", handlerIndex())
		http.Handle("/report/", http.StripPrefix("/report/",
			http.FileServer(http.Dir(base)),
		))
		log.Fatal(http.ListenAndServe(addr, nil))
		return
	}

	var report report
	if err := decodeFile(os.Args[1], &report); err != nil {
		log.Fatal(err)
	}

	var servers []string
	for s := range report {
		servers = append(servers, s)
	}
	sort.Strings(servers)

	var (
		failed bool
	)
	tw := tabwriter.NewWriter(os.Stderr, 0, 4, 1, ' ', 0)
	for _, server := range servers {
		var (
			srvFailed  bool
			hdrWritten bool
			counter    statusCounter
		)

		var cases []string
		for id := range report[server] {
			cases = append(cases, id)
		}
		sortBySegment(cases)
		for _, id := range cases {
			c := report[server][id]

			var r entryReport
			err := decodeFile(path.Join(base, c.ReportFile), &r)
			if err != nil {
				log.Fatal(err)
			}
			counter.Inc(c.Behavior)
			bad := failing(c.Behavior)
			if bad {
				srvFailed = true
				failed = true
			}
			if *verbose || bad {
				if !hdrWritten {
					hdrWritten = true
					n, _ := fmt.Fprintf(os.Stderr, "AGENT %q\n", server)
					_, _ = fmt.Fprintf(tw, "%s\n", strings.Repeat("=", n-1))
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", server, id, c.Behavior)
			}
			if bad {
				_, _ = fmt.Fprintf(tw, "\tdesc:\t%s\n", r.Description)
				_, _ = fmt.Fprintf(tw, "\texp: \t%s\n", r.Expectation)
				_, _ = fmt.Fprintf(tw, "\tact: \t%s\n", r.Result)
			}
		}
		if hdrWritten {
			_, _ = fmt.Fprint(tw, "\n")
		}
		var status string
		if srvFailed {
			status = statusFailed
		} else {
			status = statusOK
		}
		n, _ := fmt.Fprintf(tw, "AGENT %q SUMMARY (%s)\n", server, status)
		_, _ = fmt.Fprintf(tw, "%s\n", strings.Repeat("=", n-1))

		_, _ = fmt.Fprintf(tw, "TOTAL:\t%d\n", counter.Total)
		_, _ = fmt.Fprintf(tw, "%s:\t%d\n", statusOK, counter.OK)
		_, _ = fmt.Fprintf(tw, "%s:\t%d\n", statusInformational, counter.Informational)
		_, _ = fmt.Fprintf(tw, "%s:\t%d\n", statusUnimplemented, counter.Unimplemented)
		_, _ = fmt.Fprintf(tw, "%s:\t%d\n", statusNonStrict, counter.NonStrict)
		_, _ = fmt.Fprintf(tw, "%s:\t%d\n", statusUnclean, counter.Unclean)
		_, _ = fmt.Fprintf(tw, "%s:\t%d\n", statusFailed, counter.Failed)
		_, _ = fmt.Fprint(tw, "\n")
		_ = tw.Flush()
	}
	var rc int
	if failed {
		rc = 1
		_, _ = fmt.Fprintf(tw, "\n\nTEST %s\n\n", statusFailed)
	} else {
		_, _ = fmt.Fprintf(tw, "\n\nTEST %s\n\n", statusOK)
	}

	_ = tw.Flush()
	os.Exit(rc)
}

type report map[string]server

type server map[string]entry

type entry struct {
	Behavior        string `json:"behavior"`
	BehaviorClose   string `json:"behaviorClose"`
	Duration        int    `json:"duration"`
	RemoveCloseCode int    `json:"removeCloseCode"`
	ReportFile      string `json:"reportFile"`
}

type entryReport struct {
	Description string `json:"description"`
	Expectation string `json:"expectation"`
	Result      string `json:"result"`
	Duration    int    `json:"duration"`
}

//go:norace
func decodeFile(path string, x interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	d := json.NewDecoder(f)
	return d.Decode(x)
}

//go:norace
func compareBySegment(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < min(len(as), len(bs)); i++ {
		ax := mustInt(as[i])
		bx := mustInt(bs[i])
		if ax == bx {
			continue
		}
		return int(ax - bx)
	}
	return len(b) - len(a)
}

//go:norace
func mustInt(s string) int64 {
	const bits = 32 << (^uint(0) >> 63)
	x, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		panic(err)
	}
	return x
}

//go:norace
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

//go:norace
func handlerIndex() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := index.Execute(w, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Fatal(err)
			return
		}
	}
}

var index = template.Must(template.New("").Parse(`
<html>
<body>
<h1>Welcome to WebSocket test server!</h1>
<h4>Ready to Autobahn!</h4>
<a href="/report">Reports</a>
</body>
</html>
`))

//go:norace
func sortBySegment(s []string) {
	sort.Slice(s, func(i, j int) bool {
		return compareBySegment(s[i], s[j]) < 0
	})
}
