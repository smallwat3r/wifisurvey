// WiFi walking survey: measure end-to-end throughput to a server (down and up),
// signal, latency and access-point handovers as you walk a building, then find
// the weak spots. Throughput is measured with iperf3 against a server you run,
// so it reflects the real path traffic takes to its destination rather than
// just the local link.
//
//	go run wifisurvey.go survey --host HOST  # walk, label landmarks, 'q' to stop
//	go run wifisurvey.go analyse [--min-up 5] # weak spots from latest survey CSV (by up Mbps)
//
// HOST is the iperf3 server (a hostname or IP). Run `iperf3 -s` there.
// Each reading runs a download then an upload test, so it takes several seconds,
// walk slowly and pause at each spot. Type a landmark name + Enter to tag where
// you are. Needs nmcli, iw, ping, and iperf3.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	csvPath       = "survey.csv"
	iperfStreams  = 8    // parallel streams: a single stream under-reports on long paths
	downSecs      = 3    // download has headroom and ramps fast, short is fine
	defaultUpSecs = 3    // short for more readings, raise with --up-time on a clean path
	defaultPort   = 5201 // iperf3 default, override with --port
)

func usage() {
	fatal("usage:\n  wifisurvey survey  --host HOST [--port N] [--csv FILE] [--up-time SECS]\n" +
		"  wifisurvey analyse [--csv FILE] [--min-up MBPS] [--min-down MBPS] [--graph FILE]")
}

// latestSurveyCSV returns the newest survey CSV in the working directory. The
// timestamped names sort chronologically, so lexical max is the latest run.
// Falls back to the bare default, which analyse reports as missing if absent.
func latestSurveyCSV() string {
	m, _ := filepath.Glob("survey*.csv")
	if len(m) == 0 {
		return csvPath
	}
	sort.Strings(m)
	return m[len(m)-1]
}

// flagValue returns the value for --name (space- or =-separated), or def.
func flagValue(args []string, name, def string) string {
	prefix := "--" + name
	for i, a := range args {
		if a == prefix && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, prefix+"="); ok {
			return v
		}
	}
	return def
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "survey":
		host := flagValue(args, "host", "")
		if host == "" {
			fatal("survey needs --host HOST (the iperf3 server hostname or IP)")
		}
		upSecs, err := strconv.Atoi(flagValue(args, "up-time", strconv.Itoa(defaultUpSecs)))
		if err != nil || upSecs < 1 {
			fatal("--up-time must be a positive integer (seconds)")
		}
		port, err := strconv.Atoi(flagValue(args, "port", strconv.Itoa(defaultPort)))
		if err != nil || port < 1 || port > 65535 {
			fatal("--port must be a valid port (1-65535)")
		}
		// default to a timestamped file so each walk is its own CSV, never
		// appended to or overwriting a previous run
		out := flagValue(args, "csv", "")
		if out == "" {
			out = fmt.Sprintf("survey-%s.csv", time.Now().Format("2006-01-02_15-04-05"))
		}
		survey(host, out, port, upSecs)
	case "analyse":
		minUp, err := strconv.ParseFloat(flagValue(args, "min-up", "5"), 64)
		if err != nil {
			fatal("--min-up must be a number")
		}
		minDown, err := strconv.ParseFloat(flagValue(args, "min-down", "0"), 64)
		if err != nil {
			fatal("--min-down must be a number")
		}
		// default to the most recent survey CSV, since runs are timestamped
		in := flagValue(args, "csv", "")
		if in == "" {
			in = latestSurveyCSV()
		}
		analyse(in, minUp, minDown, flagValue(args, "graph", ""))
	default:
		usage()
	}
}
