package main

import (
	"bytes"
	"crypto/tls"
	"encoding/csv"
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/urfave/cli" // renamed from codegansta
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	VERSION      string  = "2016-06-17"
	UA           string  = "VGT MnM GraphiteChecker/1.0"
	DEF_TMOUT    float64 = 10.0
	DEF_PROT     string  = "http"
	DEF_ADR      string  = "graphite.wirelesscar.net"
	DEF_PERIOD   string  = "301s"
	DEF_PORT     int     = 80
	URL_TMPL     string  = "%s://%s:%d/render?target=%s&amp;format=csv&amp;from=-%s"
	CMP_LT       string  = "lt"
	CMP_GT       string  = "gt"
	G_DATEFORMAT string  = "2006-01-02 15:04:05"
	S_OK         string  = "OK"
	S_WARNING    string  = "WARNING"
	S_CRITICAL   string  = "CRITICAL"
	S_UNKNOWN    string  = "UNKNOWN"
	E_OK         int     = 0
	E_WARNING    int     = 1
	E_CRITICAL   int     = 2
	E_UNKNOWN    int     = 3
)

// Note that TS and Value have switched order here compared the format one uses for posting TO Graphite
// I don't know why it returns it in a different order than it receives it, but good to be aware of.
type Metric struct {
	Path  string
	TS    time.Time
	Value float64
}

type Metrics []*Metric

type GraphiteResponse struct {
	MS  Metrics
	RT  float64
	Err error
}

// Run debugging with not-so-light function calls through this, to avoid running
// it at all if not at debug level
//func _debug(f func()) {
//	lvl := log.GetLevel()
//	if lvl == log.DebugLevel {
//		f()
//	}
//}

// LongestKey() finds the longest metric name in a slice
func (ms Metrics) LongestKey() int {
	var l int
	for i := range ms {
		tmpl := len(ms[i].Path)
		if tmpl > l {
			l = tmpl
		}
	}
	return l
}

// Dump() prettyprints a slice of metrics
func (ms Metrics) Dump(w io.Writer, ralign int) {
	for i := range ms {
		fmt.Fprintf(w, fmt.Sprintf("%s%d%s", "%-", ralign, "s % 12.4f %d\n"), ms[i].Path, ms[i].Value, ms[i].TS.Unix())
	}
}

// FilterOffenders() splits a slice of metrics into 3 new slices based on values in regard to thresholds
func (ms Metrics) FilterOffenders(condition string, warn, crit float64) (o, w, c Metrics) {
	o = Metrics{} // those in OK state
	w = Metrics{} // those in WARNING state
	c = Metrics{} // those in CRITICAL state
	for i := range ms {
		if checkIf(condition, ms[i].Value, crit) {
			c = append(c, ms[i])
		} else if checkIf(condition, ms[i].Value, warn) {
			w = append(w, ms[i])
		} else {
			o = append(o, ms[i])
		}
	}
	if condition == CMP_GT {
		sort.Sort(sort.Reverse(o))
		sort.Sort(sort.Reverse(w))
		sort.Sort(sort.Reverse(c))
	} else {
		sort.Sort(o)
		sort.Sort(w)
		sort.Sort(c)
	}
	return o, w, c
}

// Max() returns the highest value in a slice of metrics
func (ms Metrics) Max() float64 {
	var max float64
	for i := range ms {
		if ms[i].Value > max {
			max = ms[i].Value
		}
	}
	return max
}

// Min() returns the lowest values in a slice of metrics
func (ms Metrics) Min() float64 {
	min := ms.Max()
	for i := range ms {
		if ms[i].Value < min {
			min = ms[i].Value
		}
	}
	return min
}

// Avg() returns the average value of all values in a slice of metrics
func (ms Metrics) Avg() float64 {
	l := len(ms)
	if l == 0 {
		return 0
	}
	var total float64
	for i := range ms {
		total += ms[i].Value
	}
	return total/float64(l)
}

// Latest() returns the latest/newest of 2 metrics based on its timestamp field
func (m *Metric) Latest(nm *Metric) *Metric {
	if m.TS.After(nm.TS) {
		return m
	}
	return nm
}

// Implement the sort interface for Metrics. Sort on Value field

func (ms Metrics) Len() int {
	return len(ms)
}

func (ms Metrics) Swap(i, j int) {
	ms[i], ms[j] = ms[j], ms[i]
}

func (ms Metrics) Less(i, j int) bool {
	return ms[i].Value < ms[j].Value
}


// NewMetric() creates a new Metric and return its pointer
func NewMetric(path string, ts time.Time, val float64) *Metric {
	return &Metric{
		Path:  path,
		Value: val,
		TS:    ts,
	}
}

// NewMetricFromCSV() takes a CSV record/line and tries to parse it into a *Metric
func NewMetricFromCSV(csv []string) (*Metric, error) {
	if len(csv) != 3 {
		return nil, errors.New("CSV record length != 3")
	}
	// verify path
	if csv[0] == "" {
		return nil, errors.New("Empty metric path")
	}
	// verify timestamp
	//log.Debugf("CSV date string: %s\n", csv[1])
	// See: http://stackoverflow.com/questions/14106541/go-parsing-date-time-strings-which-are-not-standard-formats
	// for an explantion of how to get date formats recognized by Go
	ts, err := time.Parse(G_DATEFORMAT, csv[1])
	if err != nil {
		return nil, err
	}
	// verify value
	if csv[2] == "" {
		return nil, errors.New("Empty value field")
	}
	val, err := strconv.ParseFloat(csv[2], 64)
	if err != nil {
		return nil, err
	}

	return NewMetric(csv[0], ts, val), nil
}

// checkIf() checks if a value is less than or bigger than a threshold based on condition/direction parameter
func checkIf(condition string, val, threshold float64) bool {
	if condition == CMP_GT {
		return val >= threshold
	}
	return val <= threshold
}

// geturl() fetches a URL and returns the HTTP response
func geturl(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("User-Agent", UA)

	tr := &http.Transport{DisableKeepAlives: true} // we're not reusing the connection, so don't let it hang open
	if strings.Index(url, "https") >= 0 {
		// Verifying certs is not the job of this plugin,
		// so we save ourselves a lot of grief by skipping any SSL verification
		// Could be a good idea for later to set this at runtime instead
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: tr}

	return client.Do(req)
}

// parse() reads a http response and converts it from CSV to Metrics if successful
// Designed to run in a separate goroutine, and hence uses a result channel instead or returning anything
func parse(url string, chRes chan GraphiteResponse) {
	gr := GraphiteResponse{}
	t_start := time.Now()
	resp, err := geturl(url)
	gr.RT = time.Duration(time.Now().Sub(t_start)).Seconds()

	if err != nil {
		gr.Err = err
		chRes <- gr
		return
	}

	defer resp.Body.Close()
	rdr := csv.NewReader(resp.Body)
	mmap := make(map[string]*Metric) // used for filtering 

	for {
		rec, err := rdr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			gr.Err = err
			break
		}
		log.Debugf("%#v", rec)
		m, err := NewMetricFromCSV(rec)
		if err != nil {
			log.Debug(err)
			continue
		}

		cm, ok := mmap[m.Path]
		if ok {
			mmap[m.Path] = m.Latest(cm) // replace existing metric with current, if newer
		} else {
			mmap[m.Path] = m // first hit, init
		}
	}

	// copy unique metrics from map to struct
	for i := range mmap {
		gr.MS = append(gr.MS, mmap[i])
	}

	chRes <- gr
}

// long_output() pretty prints 3 metric slices for usage in op5 long output on extinfo page
func long_output(o, w, c Metrics, align int) string {
	var buf bytes.Buffer
	if len(c) > 0 {
		fmt.Fprintf(&buf, "===> Metrics in state %s:\n", S_CRITICAL)
		c.Dump(&buf, align)
		fmt.Fprintf(&buf, "\n")
	}
	if len(w) > 0 {
		fmt.Fprintf(&buf, "===> Metrics in state %s:\n", S_WARNING)
		w.Dump(&buf, align)
		fmt.Fprintf(&buf, "\n")
	}
	if len(o) > 0 {
		fmt.Fprintf(&buf, "===> Metrics in state %s:\n", S_OK)
		o.Dump(&buf, align)
		fmt.Fprintf(&buf, "\n")
	}
	return buf.String()
}

// run_check() takes the CLI params and glue together all logic in the program
func run_check(c *cli.Context) {
	prot := c.String("protocol")
	host := c.String("hostname")
	port := c.Int("port")
	mpath := c.String("metricpath")
	period := c.String("timeperiod")
	tmout := c.Float64("timeout")
	condition := c.String("if")
	warn := c.Float64("warning")
	crit := c.Float64("critical")

	if condition != CMP_GT {
		condition = CMP_LT
	}

	url := fmt.Sprintf(URL_TMPL, prot, host, port, mpath, period)

	log.Debugf("URL: %s\n", url)

	chRes := make(chan GraphiteResponse)
	defer close(chRes)

	// run in parallell
	go parse(url, chRes)

	select {
	case res := <-chRes:
		if res.Err != nil {
			fmt.Printf("%s: Error parsing result: %q", S_CRITICAL, res.Err)
			os.Exit(E_CRITICAL)
		}

		align := res.MS.LongestKey()
		o, w, c := res.MS.FilterOffenders(condition, warn, crit)
		lo := long_output(o, w, c, align)
		nc := len(c)
		nw := len(w)
		no := len(o)

		// saving all values in a map to avoid running each calculation more than once
		const (
			K_A string = "avg"
			K_U string = "upper"
			K_L string = "lower"
		)
		vals := make(map[string]map[string]float64)
		vals["c"] = make(map[string]float64)
		vals["c"][K_A] = c.Avg()
		vals["c"][K_L] = c.Min()
		vals["c"][K_U] = c.Max()
		vals["w"] = make(map[string]float64)
		vals["w"][K_A] = w.Avg()
		vals["w"][K_L] = w.Min()
		vals["w"][K_U] = w.Max()
		vals["o"] = make(map[string]float64)
		vals["o"][K_A] = o.Avg()
		vals["o"][K_L] = o.Min()
		vals["o"][K_U] = o.Max()

		// helper func
		genperf := func(ecode int) string {
			perf_tmpl := "|value=%f;%f;%f;%f;%f response_time=%fs;%f;%f; num_matching_metrics=%d;"
			rt_warn := tmout / 2 // we don't really have a warning level for timeout, but only for the sake of perf output
			var str string
			// helper in helper func
			_fmt := func(key string, count int) string {
				return fmt.Sprintf(perf_tmpl, vals[key][K_A], warn, crit,
					vals[key][K_L], vals[key][K_U], res.RT, rt_warn, tmout, count)
			}
			switch ecode {
			case E_CRITICAL:
				str = _fmt("c", nc)
			case E_WARNING:
				str = _fmt("w", nw)
			case E_OK:
				str = _fmt("o", no)
			default:
				str = fmt.Sprintf(perf_tmpl, 0.0, warn, crit, 0.0, 0.0, 0, res.RT, rt_warn, tmout)
			}
			return str
		}

		// helper func
		nagios_result := func(ecode int) {
			var dw string // "direction word"
			if condition == CMP_LT {
				dw = "below"
			} else {
				dw = "above"
			}
			msg_tmpl := "%d metrics are %s the %s threshold of %.02f %s"
			var msg, status string
			if ecode == E_CRITICAL {
				status = S_CRITICAL
				msg = fmt.Sprintf(msg_tmpl, nc, dw, strings.ToLower(S_CRITICAL), crit, genperf(ecode))
			}
			if ecode == E_WARNING {
				status = S_WARNING
				msg = fmt.Sprintf(msg_tmpl, nw, dw, strings.ToLower(S_WARNING), warn, genperf(ecode))
			}
			if ecode == E_OK {
				status = S_OK
				msg = fmt.Sprintf("%d metrics at %.02f on average, min: %.02f, max: %.02f %s",
					no, vals["o"][K_A], vals["o"][K_L], vals["o"][K_U], genperf(ecode))
			}
			if ecode == E_UNKNOWN {
				status = S_UNKNOWN
				//msg = fmt.Sprintf("There's something strange in your neighbourhood, who ya gonna call?%s", genperf(ecode))
				msg = fmt.Sprintf("No values in Graphite within %s range!%s", period, genperf(ecode))
			}
			fmt.Printf("%s: %s\n\n%s", status, msg, lo)
			os.Exit(ecode)
		}

		// evaluate, print and exit
		if nc > 0 {
			nagios_result(E_CRITICAL)
		}
		if nw > 0 {
			nagios_result(E_WARNING)
		}
		if no > 0 {
			nagios_result(E_OK)
		} else {
			nagios_result(E_UNKNOWN)
		}
	case <-time.After(time.Second * time.Duration(tmout)):
		fmt.Printf("%s: Timed out after %d seconds", S_CRITICAL, int(tmout))
		os.Exit(E_CRITICAL)
	}
}

func main() {
	app := cli.NewApp()
	app.Name = "check_graphite"
	app.Version = VERSION
	app.Author = "Odd E. Ebbesen"
	app.Email = "odd.ebbesen@wirelesscar.com"
	app.Usage = "Check Graphite values and alert in Nagios/op5"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "hostname, H",
			Value: DEF_ADR,
			Usage: "Hostname or IP to check",
		},
		cli.IntFlag{
			Name:  "port, p",
			Value: DEF_PORT,
			Usage: "TCP port",
		},
		cli.StringFlag{
			Name:  "protocol, P",
			Value: DEF_PROT,
			Usage: "Protocol to use (http or https)",
		},
		cli.StringFlag{
			Name:  "metricpath, m",
			Usage: "Metric path or Graphite function",
		},
		cli.StringFlag{
			Name:  "timeperiod, T",
			Value: DEF_PERIOD,
			Usage: "Timeperiod for selection",
		},
		cli.Float64Flag{
			Name:  "warning, w",
			Usage: "Response time to result in WARNING status, in seconds",
		},
		cli.Float64Flag{
			Name:  "critical, c",
			Usage: "Response time to result in CRITICAL status, in seconds",
		},
		cli.StringFlag{
			Name:  "if, i",
			Value: CMP_GT,
			Usage: "Set whether to trigger on values being less than (lt) or greater than (gt) thresholds",
		},
		cli.Float64Flag{
			Name:  "timeout, t",
			Value: DEF_TMOUT,
			Usage: "Number of seconds before connection times out",
		},
		cli.StringFlag{
			Name:  "log-level, l",
			Value: "fatal",
			Usage: "Log level (options: debug, info, warn, error, fatal, panic)",
		},
		cli.BoolFlag{
			Name:   "debug, d",
			Usage:  "Run in debug mode",
			EnvVar: "CHECK_GRAPHITE_DEBUG",
		},
	}

	app.Before = func(c *cli.Context) error {
		log.SetOutput(os.Stdout)
		level, err := log.ParseLevel(c.String("log-level"))
		if err != nil {
			log.Fatal(err.Error())
		}
		log.SetLevel(level)
		if !c.IsSet("log-level") && !c.IsSet("l") && c.Bool("debug") {
			log.SetLevel(log.DebugLevel)
		}
		return nil
	}

	app.Action = run_check
	app.Run(os.Args)
}
