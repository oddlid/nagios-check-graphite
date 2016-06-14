package main

import (
	//"bytes"
	"crypto/tls"
	//"encoding/xml"
	"encoding/csv"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	//"io/ioutil"
	"io"
	"net/http"
	//"net/url"
	"os"
	"strings"
	"time"
)

const (
	VERSION    string  = "2016-06-14"
	UA         string  = "VGT MnM GraphiteChecker/1.0"
	DEF_TMOUT  float64 = 30.0
	DEF_PROT   string  = "http"
	DEF_ADR    string  = "graphite.wirelesscar.net"
	DEF_PERIOD string  = "301s"
	DEF_PORT   int     = 80
	URL_TMPL   string  = "%s://%s:%d/render?target=%s&amp;format=csv&amp;from=-%s"
	S_OK       string  = "OK"
	S_WARNING  string  = "WARNING"
	S_CRITICAL string  = "CRITICAL"
	S_UNKNOWN  string  = "UNKNOWN"
	E_OK       int     = 0
	E_WARNING  int     = 1
	E_CRITICAL int     = 2
	E_UNKNOWN  int     = 3
)

type Metric struct {
	Path  string
	Value float64
	TS    time.Time
}

type Metrics []Metric


// Simplify debugging
func _debug(f func()) {
	lvl := log.GetLevel()
	if lvl == log.DebugLevel {
		f()
	}
}

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

func parse(url string) {
	//t_start := time.Now()
	resp, err := geturl(url)
	//rt := time.Duration(time.Now().Sub(t_start)).Seconds()

	if err != nil {
		log.Error(err)
		//...
	}

	defer resp.Body.Close()

	rdr := csv.NewReader(resp.Body)

	for {
		rec, err := rdr.Read()
		if err == io.EOF {
			break
		}

		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("%#v", rec)
	}

	//...
}

func run_check(c *cli.Context) {
	prot := c.String("protocol")
	host := c.String("hostname")
	port := c.Int("port")
	mpath := c.String("metricpath")
	period := c.String("timeperiod")
	//tmout := c.Float64("timeout")

	url := fmt.Sprintf(URL_TMPL, prot, host, port, mpath, period)

	log.Debugf("URL: %s\n", url)

	parse(url)
}

func main() {
	app := cli.NewApp()
	app.Name = "check_graphite"
	app.Version = VERSION
	app.Author = "Odd E. Ebbesen"
	app.Email = "odd.ebbesen@wirelesscar.com"
	app.Usage = "Check Graphite values and alert in Nagios/op5"

	//...
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
			Name: "warning, w",
			//Value: defWarn,
			Usage: "Response time to result in WARNING status, in seconds",
		},
		cli.Float64Flag{
			Name: "critical, c",
			//Value: defCrit,
			Usage: "Response time to result in CRITICAL status, in seconds",
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
