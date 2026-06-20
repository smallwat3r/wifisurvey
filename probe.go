package main

import (
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// run executes a command and returns stdout, ignoring a non-zero exit.
func run(name string, args ...string) string {
	return runCtx(context.Background(), name, args...)
}

// runCtx is run with a context, so a slow command (iperf3, ping) is killed the
// moment the context is cancelled (i.e. when the user quits).
func runCtx(ctx context.Context, name string, args ...string) string {
	out, _ := exec.CommandContext(ctx, name, args...).Output()
	return string(out)
}

func wifiIface() string {
	out := run("nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "dev")
	for _, line := range strings.Split(out, "\n") {
		p := strings.Split(line, ":")
		if len(p) >= 3 && p[1] == "wifi" && strings.Contains(p[2], "connected") {
			return p[0]
		}
	}
	return ""
}

type signal struct {
	bssid, ssid string
	dbm         int
	dbmOK       bool
}

var (
	reConnected = regexp.MustCompile(`Connected to ([0-9a-fA-F:]{17})`)
	reSignal    = regexp.MustCompile(`(?i)signal:\s*(-?\d+)`)
	reSSID      = regexp.MustCompile(`(?i)SSID:\s*(.+)`)
	reTime      = regexp.MustCompile(`time=([\d.]+)`)
)

// iwSignal returns the connected AP via `iw dev link`, or nil if not associated.
func iwSignal(iface string) *signal {
	return parseIwLink(run("iw", "dev", iface, "link"))
}

// parseIwLink extracts the connected AP from `iw dev link` output. This regex
// parsing is the most fragile part of the program.
func parseIwLink(out string) *signal {
	m := reConnected.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	s := &signal{bssid: strings.ToLower(m[1])}
	if ss := reSSID.FindStringSubmatch(out); ss != nil {
		s.ssid = strings.TrimSpace(ss[1])
	}
	if sig := reSignal.FindStringSubmatch(out); sig != nil {
		if v, err := strconv.Atoi(sig[1]); err == nil {
			s.dbm, s.dbmOK = v, true
		}
	}
	return s
}

// latencyMs is the round-trip ms to host via a single ping.
func latencyMs(ctx context.Context, host string) (float64, bool) {
	if m := reTime.FindStringSubmatch(runCtx(ctx, "ping", "-c", "1", "-w", "2", host)); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return round1(v), true
		}
	}
	return 0, false
}

// iperfReport is the slice of `iperf3 -J` output we use.
type iperfReport struct {
	End struct {
		Streams []struct {
			Sender struct {
				MaxRtt float64 `json:"max_rtt"` // microseconds
			} `json:"sender"`
		} `json:"streams"`
		SumSent struct {
			Retransmits int `json:"retransmits"`
		} `json:"sum_sent"`
		SumReceived struct {
			BitsPerSecond float64 `json:"bits_per_second"`
		} `json:"sum_received"`
	} `json:"end"`
	Error string `json:"error"`
}

// iperfResult is the throughput plus, for an upload test, the loaded RTT (the
// max round-trip during the transfer, the bufferbloat signal) and retransmits
// (a packet-loss proxy). rttMs and retr are not meaningful for a download (-R).
type iperfResult struct {
	mbps, rttMs float64
	retr        int
	ok          bool
}

// iperf measures throughput to host. reverse=true is download (server to
// client), false is upload (client to server). Uses parallel streams for an
// accurate aggregate. ok=false if the server is unreachable.
func iperf(ctx context.Context, host string, port int, reverse bool, secs int) iperfResult {
	args := []string{"-c", host, "-p", strconv.Itoa(port), "-J",
		"-t", strconv.Itoa(secs), "-P", strconv.Itoa(iperfStreams)}
	if reverse {
		args = append(args, "-R")
	}
	return parseIperf(runCtx(ctx, "iperf3", args...))
}

// parseIperf extracts the result from `iperf3 -J` output. Throughput is the
// receiver's goodput, RTT the worst across streams. ok=false if the server was
// unreachable or nothing was transferred.
func parseIperf(out string) iperfResult {
	var r iperfReport
	if json.Unmarshal([]byte(out), &r) != nil || r.Error != "" {
		return iperfResult{}
	}
	mbps := round1(r.End.SumReceived.BitsPerSecond / 1e6)
	if mbps <= 0 {
		return iperfResult{}
	}
	res := iperfResult{mbps: mbps, retr: r.End.SumSent.Retransmits, ok: true}
	var maxRtt float64 // worst RTT across the streams, microseconds
	for _, s := range r.End.Streams {
		maxRtt = math.Max(maxRtt, s.Sender.MaxRtt)
	}
	res.rttMs = round1(maxRtt / 1000)
	return res
}
