package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// green/yellow/red quality palette, shared by the dBm and RTT bars and their
// legends. Stored as 0xRRGGBB ints for gnuplot's `lc rgb variable` data column,
// rgb formats one as a "#RRGGBB" literal for static script text.
const (
	cGood = 0x1a9850 // green
	cWarn = 0xe0c000 // yellow
	cBad  = 0xcc0000 // red
)

func rgb(c int) string { return fmt.Sprintf("#%06x", c) }

// rttColour maps a loaded RTT (ms) to a green/yellow/red 0xRRGGBB code for the
// chart bar: good for real-time use, usable, then poor (matches rttGrade bands).
func rttColour(ms float64) int {
	switch {
	case ms < 100:
		return cGood
	case ms < 200:
		return cWarn
	default:
		return cBad
	}
}

// dbmColour maps a signal strength (dBm) to a green/yellow/red 0xRRGGBB code by
// absolute quality, matching the chart legend: strong >= -60, ok -60 to -75,
// weak below. Absolute thresholds, not relative to the survey's own range, so a
// uniformly strong survey reads as all green.
func dbmColour(dbm float64) int {
	switch {
	case dbm >= -60:
		return cGood
	case dbm >= -75:
		return cWarn
	default:
		return cBad
	}
}

// reading is one survey row reduced to down/up against wall-clock time (unix
// seconds), plus the label and AP, for the time-series chart and its markers.
type reading struct {
	unix           float64
	down, up       float64
	dbm            float64
	rtt            float64
	hasDown, hasUp bool
	hasDbm, hasRtt bool
	label, bssid   string
}

// timeSeries parses records into per-reading down/up values against wall-clock
// time. Rows with an unparseable timestamp drop out.
func timeSeries(records [][]string) []reading {
	if len(records) < 2 {
		return nil
	}
	col := map[string]int{}
	for i, n := range records[0] {
		col[n] = i
	}
	get := func(r []string, name string) string {
		if i, ok := col[name]; ok && i < len(r) {
			return r[i]
		}
		return ""
	}
	var out []reading
	for _, r := range records[1:] {
		t, err := time.Parse("2006-01-02 15:04:05", get(r, "timestamp"))
		if err != nil {
			continue
		}
		rd := reading{unix: float64(t.Unix()), label: get(r, "label"), bssid: get(r, "bssid")}
		if v, err := strconv.ParseFloat(get(r, "mbps_down"), 64); err == nil {
			rd.down, rd.hasDown = v, true
		}
		if v, err := strconv.ParseFloat(get(r, "mbps_up"), 64); err == nil {
			rd.up, rd.hasUp = v, true
		}
		if v, err := strconv.ParseFloat(get(r, "signal_dbm"), 64); err == nil {
			rd.dbm, rd.hasDbm = v, true
		}
		if v, err := strconv.ParseFloat(get(r, "rtt_ms"), 64); err == nil {
			rd.rtt, rd.hasRtt = v, true
		}
		out = append(out, rd)
	}
	return out
}

// trendPoints smooths a direction into ~60 buckets, returning {unix seconds,
// mean Mbps} per bucket for an overlaid trend line.
func trendPoints(rs []reading, up bool) [][2]float64 {
	w := len(rs) / 60
	if w < 4 {
		w = 4
	}
	var out [][2]float64
	for i := 0; i < len(rs); i += w {
		var sumV, sumT float64
		var nV, nT int
		for _, r := range rs[i:min(i+w, len(rs))] {
			v, has := r.down, r.hasDown
			if up {
				v, has = r.up, r.hasUp
			}
			sumT += r.unix
			nT++
			if has {
				sumV += v
				nV++
			}
		}
		if nV == 0 {
			continue
		}
		out = append(out, [2]float64{sumT / float64(nT), round1(sumV / float64(nV))})
	}
	return out
}

// graph writes a self-contained gnuplot script (inline data). Run
// `gnuplot FILE.gp` to render a PDF. No extension defaults to .gp.
func graph(path string, records [][]string, meta map[string]string) {
	if filepath.Ext(path) == "" {
		path += ".gp"
	}
	pdf := strings.TrimSuffix(filepath.Base(path), ".gp") + ".pdf"
	if err := os.WriteFile(path, []byte(gnuplotScript(records, pdf, meta)), 0o644); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("Gnuplot script written to %s (run: gnuplot %s -> %s)\n", path, path, pdf)
}

// gpEscape escapes a string for a gnuplot double-quoted literal. The terminal
// runs in enhanced-text mode, so it also escapes the markup specials
// ({ } ^ _ @ & ~): a doubled backslash survives string parsing as one, which
// tells the enhanced processor to render the character literally.
func gpEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	for _, c := range []string{"{", "}", "^", "_", "@", "&", "~"} {
		s = strings.ReplaceAll(s, c, `\\`+c)
	}
	return s
}

// gnuplotScript builds the self-contained gnuplot script: throughput over time
// on a linear Mbps axis, faint down/up scatter with bold trend lines, shaded
// location bands with per-location and overall averages, and a bottom strip of
// colour-coded signal (dBm) and loaded-RTT bars marking each AP switch.
func gnuplotScript(records [][]string, pdf string, meta map[string]string) string {
	rs := timeSeries(records)
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w := func(s string) { b.WriteString(s) } // static text: no % or \n escaping

	maxV := 0.0
	hasDbm := false
	hasRtt := false
	var g struct { // overall averages across every reading, shown under the title
		downSum, upSum, dbmSum, rttSum float64
		downN, upN, dbmN, rttN         int
	}
	for _, r := range rs {
		if r.hasDown {
			maxV = math.Max(maxV, r.down)
			g.downSum, g.downN = g.downSum+r.down, g.downN+1
		}
		if r.hasUp {
			maxV = math.Max(maxV, r.up)
			g.upSum, g.upN = g.upSum+r.up, g.upN+1
		}
		if r.hasDbm {
			hasDbm = true
			g.dbmSum, g.dbmN = g.dbmSum+r.dbm, g.dbmN+1
		}
		if r.hasRtt {
			hasRtt = true
			g.rttSum, g.rttN = g.rttSum+r.rtt, g.rttN+1
		}
	}
	yMax := math.Ceil(maxV/10) * 10
	if yMax == 0 {
		yMax = 10
	}
	// the signal/RTT bars live in a reserved strip below the data (negative y),
	// so they sit above the time axis without overlaying the throughput lines
	nBar := 0
	if hasDbm {
		nBar++
	}
	if hasRtt {
		nBar++
	}
	// the strip stacks (top to bottom): per-location stats block, BSSID box, then
	// the signal/RTT bars, all below the data so nothing overlays the throughput
	band := 0.0
	switch nBar {
	case 2:
		band = yMax * 0.46
	case 1:
		band = yMax * 0.32
	}
	yLoc := -band * 0.16 // per-location name + averages, upper strip
	yName, yDbm, yRtt := 0.0, 0.0, 0.0
	switch {
	case nBar == 2:
		yName, yDbm, yRtt = -band*0.49, -band*0.61, -band*0.85
	case hasDbm:
		yName, yDbm = -band*0.58, -band*0.80
	case hasRtt:
		yName, yRtt = -band*0.58, -band*0.80
	}
	yAP := yDbm // AP-switch diamonds ride the dBm bar (or RTT bar if no signal)
	if !hasDbm {
		yAP = yRtt
	}
	lastX := int64(rs[len(rs)-1].unix)

	p("# Generated by wifisurvey. Render a vector PDF with: gnuplot %s\n", pdf[:len(pdf)-4]+".gp")
	p("set terminal pdfcairo enhanced size 9in,5in font \"Helvetica,11\"\n")
	p("set output '%s'\n\n", pdf)
	p("unset title\n")
	// title, with the iperf3 target and source CSV from the survey metadata,
	// both in one parenthesised group so they read as a consistent subtitle
	title := "Throughput over time"
	var sub []string
	if h := meta["host"]; h != "" {
		tgt := "vs iperf3 " + h
		if port := meta["port"]; port != "" {
			tgt += ":" + port
		}
		sub = append(sub, tgt)
	}
	if src := meta["source"]; src != "" {
		sub = append(sub, gpEscape(src))
	}
	if len(sub) > 0 {
		title += fmt.Sprintf("  {/*0.7 (%s)}", strings.Join(sub, " · "))
	}
	p("set label \"%s\" at screen 0.015, screen 0.965 left font \"Helvetica,13\"\n", title)
	// overall averages across the whole survey, under the title
	var parts []string
	if g.downN > 0 {
		parts = append(parts, fmt.Sprintf("↓%.0f", g.downSum/float64(g.downN)))
	}
	if g.upN > 0 {
		parts = append(parts, fmt.Sprintf("↑%.0f Mbps", g.upSum/float64(g.upN)))
	}
	if g.dbmN > 0 {
		avg := g.dbmSum / float64(g.dbmN)
		parts = append(parts, fmt.Sprintf("signal: %d%%", dbmToPct(int(math.Round(avg)))))
	}
	if g.rttN > 0 {
		avg := g.rttSum / float64(g.rttN)
		parts = append(parts, fmt.Sprintf("rtt: %s (%.0f ms)", rttGrade(avg), avg))
	}
	overall := "global: " + strings.Join(parts, ", ")
	p("set label \"%s\" at screen 0.015, screen 0.94 left font \",9\" tc rgb \"black\"\n", overall)
	// footer explaining the metrics, in the reserved space below the axis. The
	// \n are literal (rendered line breaks in the label), the % is literal too.
	p("set bmargin at screen 0.20\n")
	w(`set label "down / up: TCP throughput to the test host in Mbps, higher is better\n` +
		`dBm: WiFi signal strength (RSSI), closer to 0 is stronger, the % is link quality\n` +
		`RTT: round-trip latency measured during the upload in ms, lower is better" ` +
		`at screen 0.015, screen 0.075 left font ",8" tc rgb "#555555"` + "\n")
	w(`set xdata time
set timefmt "%s"
set format x "%H:%M:%S"
set xtics rotate by -90
set ylabel "Mbps"
`)
	yStep := math.Max(10, math.Ceil(yMax/8/10)*10)
	p("set yrange [%g:%g]\nset ytics 0,%g,%g\n", -band, yMax, yStep, yMax)
	w(`set grid ytics lc rgb "#cccccc" lw 1
set border lw 2
`)
	if band > 0 {
		// faint line at zero separating the data from the bar strip below
		w(`set arrow from graph 0, first 0 to graph 1, first 0 nohead lc rgb "#888888" lw 1 front` + "\n")
	}
	w("set key tmargin right vertical maxrows 3\n\n")
	// BSSID labels are drawn in a bright-yellow, black-bordered box (matching
	// the AP diamond marker)
	w(`set style textbox opaque fillcolor rgb "#ffe000" border rgb "black" lw 1` + "\n\n")

	block := func(name string, sel func(reading) (float64, bool)) {
		p("$%s << EOD\n", name)
		for _, r := range rs {
			if v, ok := sel(r); ok {
				p("%d %s\n", int64(r.unix), fmt1(v))
			}
		}
		p("EOD\n\n")
	}
	block("down", func(r reading) (float64, bool) { return r.down, r.hasDown })
	block("up", func(r reading) (float64, bool) { return r.up, r.hasUp })
	if hasRtt {
		// time + a discrete green/yellow/red colour by loaded-RTT quality
		p("$rtt << EOD\n")
		for _, r := range rs {
			if r.hasRtt {
				p("%d %d\n", int64(r.unix), rttColour(r.rtt))
			}
		}
		p("EOD\n\n")
	}
	downTrend, upTrend := trendPoints(rs, false), trendPoints(rs, true)
	for _, t := range []struct {
		name string
		pts  [][2]float64
	}{{"downtrend", downTrend}, {"uptrend", upTrend}} {
		p("$%s << EOD\n", t.name)
		for _, pt := range t.pts {
			p("%d %s\n", int64(pt[0]), fmt1(pt[1]))
		}
		p("EOD\n\n")
	}
	if hasDbm {
		// time + a discrete green/yellow/red colour by absolute signal quality
		p("$dbm << EOD\n")
		for _, r := range rs {
			if r.hasDbm {
				p("%d %d\n", int64(r.unix), dbmColour(r.dbm))
			}
		}
		p("EOD\n\n")
		// name the bar just past its right-hand end, level with the bar
		p("set label \"signal (dBm)\" at %d, %g left offset 1,0 tc rgb \"#333333\" font \",8\"\n\n",
			lastX, yDbm)
	}
	if hasRtt {
		p("set label \"RTT (ms)\" at %d, %g left offset 1,0 tc rgb \"#333333\" font \",8\"\n\n",
			lastX, yRtt)
	}

	// locations are regions, so shade each contiguous one as a faint band
	// (behind the data, alternating ones tinted) and name it in the middle,
	// clearer than a boundary line.
	type seg struct {
		label          string
		x0, x1         int64
		n              int     // readings in this location
		downSum, upSum float64 // average throughput shown by the name
		downN, upN     int
		dbmSum         float64 // average signal shown by the name
		dbmN           int
		rttSum         float64 // average RTT quality shown by the name
		rttN           int
	}
	var segs []seg
	for i, r := range rs {
		if i == 0 || r.label != rs[i-1].label {
			if n := len(segs); n > 0 {
				segs[n-1].x1 = int64(r.unix)
			}
			segs = append(segs, seg{label: r.label, x0: int64(r.unix), x1: int64(r.unix)})
		}
		s := &segs[len(segs)-1]
		s.n++
		if r.hasDown {
			s.downSum += r.down
			s.downN++
		}
		if r.hasUp {
			s.upSum += r.up
			s.upN++
		}
		if r.hasDbm {
			s.dbmSum += r.dbm
			s.dbmN++
		}
		if r.hasRtt {
			s.rttSum += r.rtt
			s.rttN++
		}
	}
	if n := len(segs); n > 0 {
		segs[n-1].x1 = int64(rs[len(rs)-1].unix)
	}
	obj, shade := 1, 0
	for _, s := range segs {
		if s.x1 <= s.x0 {
			continue // zero-width band can't render
		}
		if shade%2 == 0 {
			p("set object %d rectangle from %d, graph 0 to %d, graph 1 fc rgb \"#fcf6d8\" fs transparent solid 0.35 noborder behind\n",
				obj, s.x0, s.x1)
			obj++
		}
		shade++
	}

	// AP change: a diamond on the signal bar, the BSSID named just above it.
	// The first reading's AP is marked too.
	// alternate AP labels above the bar then below the diamond, so two switches
	// close in time don't print their names on top of each other
	yAbove, yBelow := "graph 0.93", "graph 0.86"
	if nBar > 0 {
		yAbove = fmt.Sprintf("%g", yName)
		yBelow = fmt.Sprintf("%g", yAP)
	}
	var ap strings.Builder
	prevBssid := ""
	nAP := 0
	for _, r := range rs {
		if r.bssid != "" && r.bssid != prevBssid {
			t := int64(r.unix)
			fmt.Fprintf(&ap, "%d %g\n", t, yAP)
			y, off := yAbove, "0,0"
			if nAP%2 == 1 {
				y, off = yBelow, "0,-1.2" // drop below the diamond
			}
			p("set label \"%s\" at %d, %s center boxed offset %s tc rgb \"black\" font \",7\"\n",
				gpEscape(r.bssid), t, y, off)
			nAP++
		}
		prevBssid = r.bssid
	}
	hasAP := ap.Len() > 0
	p("$ap << EOD\n%sEOD\n\n", ap.String())

	// connectivity gaps: rows with no throughput (no WiFi, or host unreachable).
	// Marked with a cross on the zero line so dead spots are obvious at a glance.
	var outage strings.Builder
	for _, r := range rs {
		if !r.hasDown && !r.hasUp {
			fmt.Fprintf(&outage, "%d 0\n", int64(r.unix))
		}
	}
	hasOutage := outage.Len() > 0
	p("$outage << EOD\n%sEOD\n\n", outage.String())

	// location names emitted last so they draw on top of the AP boxes and bar,
	// with reading count and the location's average signal under the name
	for _, s := range segs {
		if s.x1 <= s.x0 {
			continue // zero-width band can't render
		}
		name := s.label
		if name == "" {
			name = "(unspecified)" // unlabelled readings are a location of their own
		}
		// compact labels so narrow (few-reading) bands don't overlap: reading
		// count beside the name, arrows for down/up, signal as % only
		var tp []string
		if s.downN > 0 {
			tp = append(tp, fmt.Sprintf("↓%.0f", s.downSum/float64(s.downN)))
		}
		if s.upN > 0 {
			tp = append(tp, fmt.Sprintf("↑%.0f", s.upSum/float64(s.upN)))
		}
		l1 := ""
		if len(tp) > 0 {
			l1 = strings.Join(tp, " ") + " Mbps"
		}
		// signal and rtt each on their own line so narrow bands stay compact
		l2 := ""
		if s.dbmN > 0 {
			l2 = fmt.Sprintf("signal: %d%%", dbmToPct(int(math.Round(s.dbmSum/float64(s.dbmN)))))
		}
		l3 := ""
		if s.rttN > 0 {
			avg := s.rttSum / float64(s.rttN)
			l3 = fmt.Sprintf("rtt: %s (%.0f ms)", rttGrade(avg), avg)
		}
		txt := fmt.Sprintf("{/:Bold %s}", gpEscape(name))
		for _, l := range []string{fmt.Sprintf("#%d", s.n), l1, l2, l3} {
			if l != "" {
				txt += "\\n" + l
			}
		}
		// in the strip above the signal bar when there is one, else top of plot
		yLab := "graph 0.95"
		if nBar > 0 {
			yLab = fmt.Sprintf("%g", yLoc)
		}
		p("set label \"%s\" at %d, %s center tc rgb \"#8a6d00\" font \",5\"\n",
			txt, (s.x0+s.x1)/2, yLab)
	}
	p("\n")

	var elems []string
	if hasDbm {
		// dBm signal bar in the bottom strip, green/yellow/red by quality (own colour column)
		elems = append(elems, fmt.Sprintf(`  $dbm using 1:(%g):2 with lines lw 8 lc rgb variable notitle`, yDbm))
	}
	if hasRtt {
		// loaded RTT bar below it, green/yellow/red by quality (own colour column)
		elems = append(elems, fmt.Sprintf(`  $rtt using 1:(%g):2 with lines lw 8 lc rgb variable notitle`, yRtt))
	}
	elems = append(elems,
		`  $down using 1:2 with points pt 7 ps 0.3 lc rgb "#8000b8d4" notitle`,
		`  $up using 1:2 with points pt 7 ps 0.3 lc rgb "#80ff1493" notitle`,
		`  $downtrend using 1:2 smooth mcsplines with lines lw 4 lc rgb "#0033cc" title "down"`,
		`  $uptrend using 1:2 smooth mcsplines with lines lw 4 lc rgb "#cc0000" title "up"`,
	)
	if hasAP {
		// filled diamond, black border + bright-yellow fill (a larger black
		// point then a smaller yellow one on top), sitting on the dBm bar
		elems = append(elems,
			`  $ap using 1:2 with points pt 13 ps 1.3 lc rgb "black" notitle`,
			`  $ap using 1:2 with points pt 13 ps 0.9 lc rgb "#ffe000" notitle`,
			`  keyentry with points pt 13 ps 1.3 lc rgb "#ffe000" title "AP switch"`)
	}
	if hasOutage {
		// red crosses on the zero line where there was no usable connection
		elems = append(elems,
			fmt.Sprintf(`  $outage using 1:2 with points pt 2 ps 0.7 lc rgb "%s" notitle`, rgb(cBad)),
			fmt.Sprintf(`  keyentry with points pt 2 ps 0.9 lc rgb "%s" title "no connection"`, rgb(cBad)))
	}
	if hasDbm {
		// dBm strength key: three buckets matching the palette colours
		elems = append(elems,
			fmt.Sprintf(`  keyentry with lines lw 8 lc rgb "%s" title "signal strong (>= -60 dBm)"`, rgb(cGood)),
			fmt.Sprintf(`  keyentry with lines lw 8 lc rgb "%s" title "signal ok (-60 to -75)"`, rgb(cWarn)),
			fmt.Sprintf(`  keyentry with lines lw 8 lc rgb "%s" title "signal weak (< -75)"`, rgb(cBad)))
	}
	if hasRtt {
		// loaded RTT key: three buckets matching the bar colours
		elems = append(elems,
			fmt.Sprintf(`  keyentry with lines lw 8 lc rgb "%s" title "RTT good (< 100 ms)"`, rgb(cGood)),
			fmt.Sprintf(`  keyentry with lines lw 8 lc rgb "%s" title "RTT ok (100-200)"`, rgb(cWarn)),
			fmt.Sprintf(`  keyentry with lines lw 8 lc rgb "%s" title "RTT bad (>= 200)"`, rgb(cBad)))
	}
	p("\nplot \\\n%s\n", strings.Join(elems, ", \\\n"))
	return b.String()
}
