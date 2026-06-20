package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var fields = []string{
	"timestamp", "label", "ssid", "bssid", "signal_dbm", "signal_pct",
	"mbps_down", "mbps_up", "rtt_ms", "retr", "latency_ms", "note",
}

func survey(host, path string, port, upSecs int) {
	iface := wifiIface()
	if iface == "" {
		fatal("No connected WiFi device found.")
	}
	checkHeader(path)
	fmt.Printf("Survey on %s to %s, ~%ds per reading. Walk slowly.\n",
		iface, host, downSecs+upSecs)
	fmt.Println("Type a landmark name + Enter to tag your location, 'q' to stop.")
	fmt.Println()
	fmt.Printf("%-9s%-12s%-18s%7s%7s%8s%6s%6s%5s%5s  note\n",
		"time", "where", "bssid", "down", "up", "rtt", "qual", "retr", "dBm", "%")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fatal(err.Error())
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if info, _ := f.Stat(); info.Size() == 0 {
		// metadata comment line (skipped on read) so the chart knows the target
		fmt.Fprintf(f, "# host=%s port=%d streams=%d down=%ds up=%ds started=%s\n",
			host, port, iperfStreams, downSecs, upSecs, time.Now().Format(time.RFC3339))
		w.Write(fields)
		w.Flush()
	}

	// Handle waypoint input on its own goroutine so labels register instantly
	// and 'q' cancels the context, aborting any in-flight iperf3/ping.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	where := ""
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "q" || line == "quit" || line == "exit" {
				cancel()
				return
			}
			mu.Lock()
			where = line
			mu.Unlock()
			fmt.Printf("  -> %s\n", dashWord(line, "(cleared)"))
		}
		cancel() // stdin closed (Ctrl-D)
	}()

	last := ""
	for ctx.Err() == nil {
		now := time.Now()
		link := iwSignal(iface)
		if link == nil {
			fmt.Printf("%-9s(not connected to WiFi)\n", now.Format("15:04:05"))
			last = ""
			sleep(ctx, time.Second)
			continue
		}

		down := iperf(ctx, host, port, true, downSecs)
		up := iperf(ctx, host, port, false, upSecs)
		lat, latOK := latencyMs(ctx, host)
		if ctx.Err() != nil {
			break // quit mid-measurement: don't log a partial/aborted row
		}
		dbmCSV, pctCSV, pctDisp := "", "", "-"
		if link.dbmOK {
			dbmCSV = strconv.Itoa(link.dbm)
			pctCSV = strconv.Itoa(dbmToPct(link.dbm))
			pctDisp = pctCSV + "%"
		}
		downCSV, upCSV, latCSV := blank(down.mbps, down.ok), blank(up.mbps, up.ok), blank(lat, latOK)
		rttCSV, retrCSV := "", "" // loaded RTT and retransmits, from the upload test
		if up.ok {
			rttCSV, retrCSV = blank(up.rttMs, up.rttMs > 0), strconv.Itoa(up.retr)
		}
		mu.Lock()
		cur := where
		mu.Unlock()

		note := ""
		if last != "" && link.bssid != last {
			note = "roam from " + last // switched AP, destination is the bssid column
		}

		w.Write([]string{now.Format("2006-01-02 15:04:05"), cur, link.ssid,
			link.bssid, dbmCSV, pctCSV, downCSV, upCSV, rttCSV, retrCSV, latCSV, note})
		w.Flush()

		fmt.Printf("%-9s%-12s%-18s%7s%7s%8s%6s%6s%5s%5s  %s\n", now.Format("15:04:05"),
			truncate(cur, 11), link.bssid, dash(downCSV), dash(upCSV),
			dash(rttCSV), rttGrade(up.rttMs), dash(retrCSV), dash(dbmCSV), pctDisp, note)
		last = link.bssid
	}
	fmt.Printf("\nSaved to %s\n", path)
}

// checkHeader refuses to append to a CSV written in a different column layout.
func checkHeader(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // missing file is fine, it'll be created with a fresh header
	}
	defer f.Close()
	r := bufio.NewReader(f)
	header, _ := r.ReadString('\n')
	for strings.HasPrefix(header, "#") { // skip the metadata line
		header, _ = r.ReadString('\n')
	}
	if h := strings.TrimSpace(header); h != "" && h != strings.Join(fields, ",") {
		fatal(path + " has an old/different column layout. " +
			"Remove or rename it, then re-run.")
	}
}

// csvMeta reads the leading "# key=value ..." comment line into a map, empty if
// none. It records the survey's iperf3 target so the chart can name it.
func csvMeta(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#") {
			break
		}
		for _, tok := range strings.Fields(strings.TrimLeft(line, "# ")) {
			if k, v, ok := strings.Cut(tok, "="); ok {
				m[k] = v
			}
		}
	}
	return m
}
