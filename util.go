package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

// dbmToPct is NetworkManager-style quality % from dBm, clamped 0..100.
func dbmToPct(dbm int) int {
	pct := int(math.Round(2 * float64(dbm+100)))
	return max(0, min(100, pct))
}

// rttGrade rates a loaded RTT (ms) for real-time use, the latency analogue of
// dbmToPct: good <100, ok <200, poor <400, bad otherwise. "-" if unknown.
func rttGrade(ms float64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 100:
		return "good"
	case ms < 200:
		return "ok"
	case ms < 400:
		return "poor"
	default:
		return "bad"
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// sleep waits d, but returns early if the context is cancelled (user quit).
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func fmt1(v float64) string { return strconv.FormatFloat(round1(v), 'f', 1, 64) }

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// dash renders an empty field as "-" for the live display.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func blank(v float64, ok bool) string {
	if !ok {
		return ""
	}
	return fmt1(v)
}

func dashWord(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func mean[T int | float64](xs []T) float64 {
	var sum T
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}

// columns indexes a CSV header row and returns a getter that reads a named
// column from any record by name, yielding "" for a missing column or short
// row. Lets the readers locate fields by header rather than fixed position.
func columns(header []string) func(r []string, name string) string {
	col := map[string]int{}
	for i, n := range header {
		col[n] = i
	}
	return func(r []string, name string) string {
		if i, ok := col[name]; ok && i < len(r) {
			return r[i]
		}
		return ""
	}
}
