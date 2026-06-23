package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// spotStat is the aggregated result for one location label.
type spotStat struct {
	label                  string
	n, aps, retr           int // readings, distinct BSSIDs, total upload retransmits
	hasDown, hasUp, hasDbm bool
	avgDown, minDown       float64
	avgUp, minUp           float64
	avgRtt                 float64 // typical loaded RTT (ms), 0 if none
	avgDbm                 int
}

var errNoData = errors.New("no data in CSV")

// summarize aggregates survey records (including the header row) into per-label
// stats, sorted worst first by upload.
func summarize(records [][]string) ([]spotStat, error) {
	if len(records) < 2 {
		return nil, errNoData
	}
	col := map[string]int{}
	for i, name := range records[0] {
		col[name] = i
	}
	// get reads a named column safely, missing column or short row yields "".
	get := func(r []string, name string) string {
		if i, ok := col[name]; ok && i < len(r) {
			return r[i]
		}
		return ""
	}
	type agg struct {
		down, up, rtt []float64
		dbm           []int
		aps           map[string]struct{}
		n, retr       int
	}
	groups := map[string]*agg{}
	for _, r := range records[1:] {
		lbl := dashWord(get(r, "label"), "(unlabelled)")
		a := groups[lbl]
		if a == nil {
			a = &agg{aps: map[string]struct{}{}}
			groups[lbl] = a // ensure every label appears, even if a test failed
		}
		a.n++
		if v, err := strconv.ParseFloat(get(r, "mbps_down"), 64); err == nil {
			a.down = append(a.down, v)
		}
		if v, err := strconv.ParseFloat(get(r, "mbps_up"), 64); err == nil {
			a.up = append(a.up, v)
		}
		if v, err := strconv.ParseFloat(get(r, "rtt_ms"), 64); err == nil {
			a.rtt = append(a.rtt, v)
		}
		if v, err := strconv.Atoi(get(r, "retr")); err == nil {
			a.retr += v
		}
		if v, err := strconv.Atoi(get(r, "signal_dbm")); err == nil {
			a.dbm = append(a.dbm, v)
		}
		if b := get(r, "bssid"); b != "" {
			a.aps[b] = struct{}{}
		}
	}
	out := make([]spotStat, 0, len(groups))
	for lbl, a := range groups {
		s := spotStat{label: lbl, n: a.n, aps: len(a.aps), retr: a.retr}
		if len(a.down) > 0 {
			s.hasDown = true
			s.avgDown, s.minDown = round1(mean(a.down)), slices.Min(a.down)
		}
		if len(a.up) > 0 {
			s.hasUp = true
			s.avgUp, s.minUp = round1(mean(a.up)), slices.Min(a.up)
		}
		if len(a.rtt) > 0 {
			s.avgRtt = round1(mean(a.rtt)) // typical loaded latency here
		}
		if len(a.dbm) > 0 {
			s.hasDbm = true
			s.avgDbm = int(math.Round(meanInt(a.dbm)))
		}
		out = append(out, s)
	}
	// worst first by average upload (the streaming direction), no upload to top
	key := func(s spotStat) float64 {
		if !s.hasUp {
			return -1
		}
		return s.avgUp
	}
	sort.Slice(out, func(i, j int) bool {
		if ki, kj := key(out[i]), key(out[j]); ki != kj {
			return ki < kj
		}
		return out[i].label < out[j].label
	})
	return out, nil
}

func analyse(path string, minUp, minDown float64, graphPath string) {
	f, err := os.Open(path)
	if err != nil {
		fatal(err.Error())
	}
	defer f.Close()
	rd := csv.NewReader(f)
	rd.Comment = '#' // ignore the metadata line
	records, err := rd.ReadAll()
	if err != nil {
		fatal("No data in CSV.")
	}
	stats, err := summarize(records)
	if err == errNoData {
		fatal("No data in CSV.")
	}

	fmt.Printf("%-14s%8s%8s%8s%8s%8s%6s%6s%6s%5s%4s%5s  status\n", "where",
		"dn avg", "dn min", "up avg", "up min", "rtt avg", "qual", "retr", "dBm", "%", "n", "APs")
	var weak []string
	for _, s := range stats {
		bad := weakSpot(s, minUp, minDown)
		status := "ok"
		switch {
		case !s.hasUp:
			status = "no up"
		case bad:
			status = "WEAK"
		}
		if bad {
			weak = append(weak, s.label)
		}
		dbmDisp, pctDisp := "-", "-"
		if s.hasDbm {
			dbmDisp = strconv.Itoa(s.avgDbm)
			pctDisp = strconv.Itoa(dbmToPct(s.avgDbm)) + "%"
		}
		rttDisp := "-"
		if s.avgRtt > 0 {
			rttDisp = fmt1(s.avgRtt)
		}
		fmt.Printf("%-14s%8s%8s%8s%8s%8s%6s%6d%6s%5s%4d%5d  %s\n",
			s.label, mbps(s.hasDown, s.avgDown), mbps(s.hasDown, s.minDown),
			mbps(s.hasUp, s.avgUp), mbps(s.hasUp, s.minUp),
			rttDisp, rttGrade(s.avgRtt), s.retr, dbmDisp, pctDisp, s.n, s.aps, status)
	}
	crit := fmt.Sprintf("%g Mbps up", minUp)
	if minDown > 0 {
		crit += fmt.Sprintf(" or %g Mbps down", minDown)
	}
	fmt.Printf("\n%d weak/unmeasured spot(s) below %s: %s\n",
		len(weak), crit, dashWord(strings.Join(weak, ", "), "none"))
	if graphPath != "" {
		meta := csvMeta(path)
		if meta == nil {
			meta = map[string]string{}
		}
		meta["source"] = filepath.Base(path)
		graph(graphPath, records, meta)
	}
}

// weakSpot reports whether a spot fails the thresholds: no upload at all,
// average upload below minUp, or (when minDown>0) average download below
// minDown or absent.
func weakSpot(s spotStat, minUp, minDown float64) bool {
	if !s.hasUp || s.avgUp < minUp {
		return true
	}
	return minDown > 0 && (!s.hasDown || s.avgDown < minDown)
}

func mbps(ok bool, v float64) string {
	if !ok {
		return "-"
	}
	return fmt1(v)
}
