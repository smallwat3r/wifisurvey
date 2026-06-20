package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGnuplotScript(t *testing.T) {
	records := [][]string{
		{"timestamp", "label", "bssid", "signal_dbm", "mbps_down", "mbps_up", "rtt_ms"},
		{"2026-06-19 12:00:00", "office", "aa", "-45", "30", "8", "40"},
		{"2026-06-19 12:00:05", "office", "aa", "-50", "28", "7", "50"},
		{"2026-06-19 12:00:10", "hall", "bb", "-70", "20", "5", "180"}, // location and AP both change
	}
	s := gnuplotScript(records, "out.pdf", map[string]string{"host": "iperf.lan", "port": "5201"})
	for _, want := range []string{
		"set terminal pdfcairo", "set output 'out.pdf'", "set border lw 2",
		"$down << EOD", "$uptrend << EOD", "$dbm << EOD", "$ap << EOD",
		"set label",
		"office", "bb", "with points pt 13", `title "down"`, `title "AP switch"`,
		"vs iperf3 iperf.lan:5201", // survey target in the title
		"global:",                  // overall averages line
		"{/:Bold office}",          // bold location name
		"$rtt << EOD",              // loaded-RTT bar present when rtt data exists
		"lc rgb variable", "RTT (ms)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("gnuplot script missing %q", want)
		}
	}
}

func TestGnuplotScriptNoMeta(t *testing.T) {
	// without metadata the title carries no target and must not break
	records := [][]string{
		{"timestamp", "label", "mbps_down", "mbps_up"},
		{"2026-06-19 12:00:00", "office", "30", "8"},
	}
	s := gnuplotScript(records, "out.pdf", nil)
	if !strings.Contains(s, "Throughput over time") {
		t.Error("missing title")
	}
	if strings.Contains(s, "vs iperf3") {
		t.Error("no metadata, but a target leaked into the title")
	}
}

func TestGpEscape(t *testing.T) {
	cases := map[string]string{
		`office`:  `office`,
		`a"b`:     `a\"b`,
		`back\h`:  `back\\h`,
		`level_2`: `level\\_2`, // underscore escaped for enhanced-text mode
		`x{y}`:    `x\\{y\\}`,
	}
	for in, want := range cases {
		if got := gpEscape(in); got != want {
			t.Errorf("gpEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRttColour(t *testing.T) {
	cases := []struct {
		ms   float64
		want int
	}{
		{0, 0x1a9850}, {99, 0x1a9850}, // good -> green
		{100, 0xe0c000}, {199, 0xe0c000}, // ok -> yellow
		{200, 0xcc0000}, {640, 0xcc0000}, // bad -> red
	}
	for _, c := range cases {
		if got := rttColour(c.ms); got != c.want {
			t.Errorf("rttColour(%v) = %#x, want %#x", c.ms, got, c.want)
		}
	}
}

func TestCsvMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.csv")
	body := "# host=iperf.lan port=5201 streams=8 started=2026-06-19T14:00:00\n" +
		"timestamp,label\n2026-06-19 12:00:00,office\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m := csvMeta(path)
	if m["host"] != "iperf.lan" || m["port"] != "5201" || m["streams"] != "8" {
		t.Errorf("meta = %v", m)
	}

	// a file with no comment line yields an empty (non-nil) map
	plain := filepath.Join(t.TempDir(), "p.csv")
	if err := os.WriteFile(plain, []byte("timestamp,label\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := csvMeta(plain); len(m) != 0 {
		t.Errorf("plain file meta = %v, want empty", m)
	}
}

func TestDbmToPct(t *testing.T) {
	cases := []struct {
		dbm, want int
	}{
		{-50, 100}, {-60, 80}, {-67, 66}, {-75, 50}, {-100, 0},
		{-30, 100}, // clamped high
		{-110, 0},  // clamped low
	}
	for _, c := range cases {
		if got := dbmToPct(c.dbm); got != c.want {
			t.Errorf("dbmToPct(%d) = %d, want %d", c.dbm, got, c.want)
		}
	}
}

func TestFlagValue(t *testing.T) {
	args := []string{"--ssid", "Net", "--min=15", "--csv", "f.csv"}
	if v := flagValue(args, "ssid", "x"); v != "Net" {
		t.Errorf("space form = %q", v)
	}
	if v := flagValue(args, "min", "10"); v != "15" {
		t.Errorf("=form = %q", v)
	}
	if v := flagValue(args, "url", "def"); v != "def" {
		t.Errorf("default = %q", v)
	}
	if v := flagValue([]string{"--ssid"}, "ssid", "d"); v != "d" {
		t.Errorf("trailing flag with no value = %q, want default", v)
	}
}

func TestParseIwLink(t *testing.T) {
	out := `Connected to 60:8d:26:53:de:ea (on wlp195s0)
        SSID: BT-RZA3ZX
        freq: 5180.0
        signal: -38 dBm
        rx bitrate: 780.0 MBit/s
        tx bitrate: 866.7 MBit/s`
	s := parseIwLink(out)
	if s == nil {
		t.Fatal("expected a signal, got nil")
	}
	if s.bssid != "60:8d:26:53:de:ea" {
		t.Errorf("bssid = %q", s.bssid)
	}
	if s.ssid != "BT-RZA3ZX" {
		t.Errorf("ssid = %q", s.ssid)
	}
	if !s.dbmOK || s.dbm != -38 {
		t.Errorf("dbm = %d ok=%v, want -38 true", s.dbm, s.dbmOK)
	}
	if parseIwLink("Not connected.") != nil {
		t.Error("expected nil when not associated")
	}
}

func TestSummarize(t *testing.T) {
	h := []string{"timestamp", "label", "ssid", "bssid", "signal_dbm",
		"signal_pct", "mbps_down", "mbps_up", "rtt_ms", "retr", "latency_ms", "note"}
	records := [][]string{
		h,
		{"t", "office", "X", "aa:01", "-37", "86", "92.0", "30.4", "45.0", "2", "24", ""},
		{"t", "office", "X", "aa:01", "-40", "80", "88.0", "28.0", "60.0", "8", "26", ""},
		{"t", "hall", "X", "aa:01", "-60", "66", "40.0", "12.0", "180.0", "30", "30", ""},
		// upload test failed here, and a different AP was seen (a roam)
		{"t", "hall", "X", "bb:02", "-66", "68", "35.0", "", "", "", "31", "roam from aa:01"},
		// dead spot: both directions failed
		{"t", "basement", "X", "cc:03", "-85", "30", "", "", "", "", "", ""},
	}
	stats, err := summarize(records)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]spotStat{}
	for _, s := range stats {
		m[s.label] = s
	}

	office := m["office"]
	if office.n != 2 || office.aps != 1 || !office.hasUp || !office.hasDown {
		t.Errorf("office: n=%d aps=%d hasUp=%v hasDown=%v",
			office.n, office.aps, office.hasUp, office.hasDown)
	}
	if office.avgUp != 29.2 || office.minUp != 28.0 {
		t.Errorf("office up: avg=%v min=%v", office.avgUp, office.minUp)
	}
	if office.avgDown != 90.0 || office.minDown != 88.0 {
		t.Errorf("office down: avg=%v min=%v", office.avgDown, office.minDown)
	}
	if office.avgDbm != -39 { // mean(-37,-40)=-38.5, rounded away from zero
		t.Errorf("office avgDbm = %d, want -39", office.avgDbm)
	}

	if office.avgRtt != 52.5 { // mean of 45 and 60
		t.Errorf("office avgRtt = %v, want 52.5", office.avgRtt)
	}
	if office.retr != 10 { // 2 + 8
		t.Errorf("office retr = %d, want 10", office.retr)
	}

	hall := m["hall"]
	if hall.n != 2 {
		t.Errorf("hall n = %d, want 2 (both readings count)", hall.n)
	}
	if hall.aps != 2 {
		t.Errorf("hall aps = %d, want 2 (two distinct BSSIDs)", hall.aps)
	}
	if !hall.hasUp || hall.minUp != 12.0 { // only one row had an upload value
		t.Errorf("hall up: hasUp=%v min=%v, want true 12", hall.hasUp, hall.minUp)
	}
	if hall.avgRtt != 180.0 || hall.retr != 30 {
		t.Errorf("hall rtt=%v retr=%d, want 180 30", hall.avgRtt, hall.retr)
	}

	basement := m["basement"]
	if basement.hasUp || basement.hasDown || basement.n != 1 || !basement.hasDbm {
		t.Errorf("basement: n=%d hasUp=%v hasDown=%v hasDbm=%v",
			basement.n, basement.hasUp, basement.hasDown, basement.hasDbm)
	}

	// worst first by upload: basement (no up) before hall (12) before office (28)
	if stats[0].label != "basement" || stats[1].label != "hall" || stats[2].label != "office" {
		t.Errorf("sort order = %s,%s,%s; want basement,hall,office",
			stats[0].label, stats[1].label, stats[2].label)
	}
}

func TestSummarizeColumnOrderAndUnlabelled(t *testing.T) {
	// Columns deliberately reordered and trimmed: summarize must locate them by
	// header name, not position. Empty labels group under "(unlabelled)", which
	// is what early readings get before a landmark is typed.
	records := [][]string{
		{"mbps_up", "bssid", "label", "signal_dbm"},
		{"25.0", "aa", "", "-50"},
		{"27.0", "aa", "", "-52"},
	}
	stats, err := summarize(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 spot, got %d", len(stats))
	}
	s := stats[0]
	if s.label != "(unlabelled)" {
		t.Errorf("label = %q, want (unlabelled)", s.label)
	}
	if s.n != 2 || s.avgUp != 26.0 || s.aps != 1 {
		t.Errorf("n=%d avgUp=%v aps=%d, want 2 26 1", s.n, s.avgUp, s.aps)
	}
}

func TestRttGrade(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0, "-"}, {-5, "-"}, // unknown
		{50, "good"}, {99, "good"},
		{100, "ok"}, {199, "ok"},
		{200, "poor"}, {399, "poor"},
		{400, "bad"}, {640, "bad"},
	}
	for _, c := range cases {
		if got := rttGrade(c.ms); got != c.want {
			t.Errorf("rttGrade(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestParseIperf(t *testing.T) {
	// upload result: receiver goodput, worst stream RTT (us -> ms), retransmits
	good := `{"end":{"streams":[{"sender":{"max_rtt":120000}},
		{"sender":{"max_rtt":250000}}],"sum_sent":{"retransmits":7},
		"sum_received":{"bits_per_second":8400000}}}`
	r := parseIperf(good)
	if !r.ok || r.mbps != 8.4 || r.rttMs != 250.0 || r.retr != 7 {
		t.Errorf("parseIperf good = %+v, want mbps 8.4 rtt 250 retr 7 ok", r)
	}
	if parseIperf(`{"error":"unable to connect to server"}`).ok {
		t.Error("error JSON should give ok=false")
	}
	if parseIperf("not json").ok {
		t.Error("garbage should give ok=false")
	}
	if parseIperf(`{"end":{"sum_received":{"bits_per_second":0}}}`).ok {
		t.Error("zero throughput should give ok=false")
	}
}

func TestWeakSpot(t *testing.T) {
	up := func(avg float64) spotStat { return spotStat{hasUp: true, avgUp: avg} }
	cases := []struct {
		name         string
		s            spotStat
		minUp, minDn float64
		want         bool
	}{
		{"good upload", up(8), 5, 0, false},
		{"low upload", up(3), 5, 0, true},
		{"no upload", spotStat{}, 5, 0, true},
		{"low download ignored when min-down off", spotStat{hasUp: true, avgUp: 8, hasDown: true, avgDown: 2}, 5, 0, false},
		{"low download flagged when min-down on", spotStat{hasUp: true, avgUp: 8, hasDown: true, avgDown: 2}, 5, 10, true},
		{"good download passes min-down", spotStat{hasUp: true, avgUp: 8, hasDown: true, avgDown: 30}, 5, 10, false},
		{"missing download flagged when min-down on", spotStat{hasUp: true, avgUp: 8}, 5, 10, true},
	}
	for _, c := range cases {
		if got := weakSpot(c.s, c.minUp, c.minDn); got != c.want {
			t.Errorf("%s: weakSpot = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTimeSeries(t *testing.T) {
	records := [][]string{
		{"timestamp", "mbps_down", "mbps_up"},
		{"2026-06-19 12:00:00", "30", "8"},
		{"2026-06-19 12:00:10", "", "6"}, // download failed
		{"not-a-time", "20", "5"},        // unparseable timestamp, dropped
	}
	ts := timeSeries(records)
	if len(ts) != 2 {
		t.Fatalf("len = %d, want 2 (bad timestamp dropped)", len(ts))
	}
	t0, _ := time.Parse("2006-01-02 15:04:05", "2026-06-19 12:00:00")
	if ts[0].unix != float64(t0.Unix()) || !ts[0].hasDown || ts[0].down != 30 || ts[0].up != 8 {
		t.Errorf("ts[0] = %+v", ts[0])
	}
	if ts[1].unix != float64(t0.Unix()+10) || ts[1].hasDown || !ts[1].hasUp || ts[1].up != 6 {
		t.Errorf("ts[1] = %+v, want +10s, no down, up 6", ts[1])
	}
}

func TestSummarizeErrors(t *testing.T) {
	if _, err := summarize([][]string{{"only", "header"}}); err != errNoData {
		t.Errorf("header-only: err = %v, want errNoData", err)
	}
	old := [][]string{{"timestamp", "label", "ssid", "mbps"}, {"t", "x", "Y", "9"}}
	if _, err := summarize(old); err != errOldFormat {
		t.Errorf("old single-mbps format: err = %v, want errOldFormat", err)
	}
}
