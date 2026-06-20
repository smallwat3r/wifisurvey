package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

// screen drives a fixed input line at the bottom of the terminal with the
// survey log scrolling in the region above it. It uses a DECSTBM scroll region
// so the prompt stays put while readings stream in. A nil *screen means stdin
// is not a TTY (piped, tests), in which case it falls back to plain stdout and
// a line scanner, so the tool still works headless.
type screen struct {
	mu       sync.Mutex
	rows     int
	buf      []byte // current input line, echoed by us since raw mode won't
	oldState *term.State
}

// newScreen enters raw mode and reserves the bottom line. Returns nil (caller
// falls back to cooked I/O) if stdin is not a terminal or setup fails.
func newScreen() *screen {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	_, rows, err := term.GetSize(fd)
	if err != nil || rows < 3 {
		return nil
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil
	}
	s := &screen{rows: rows, oldState: old}
	// scroll region = rows 1..rows-1, leaving the last row for the prompt
	fmt.Printf("\033[1;%dr", rows-1)
	s.drawPrompt()
	return s
}

// restore resets the scroll region and leaves raw mode. Safe on a nil screen.
func (s *screen) restore() {
	if s == nil || s.oldState == nil {
		return // nil screen, or already restored (idempotent: explicit + defer)
	}
	fmt.Printf("\033[r\033[%d;1H\r\n", s.rows) // drop region, move below the bar
	term.Restore(int(os.Stdin.Fd()), s.oldState)
	s.oldState = nil
}

// line prints one log line above the prompt, scrolling the region, then redraws
// the prompt so the cursor stays with the user's input.
func (s *screen) line(text string) {
	if s == nil {
		fmt.Println(text)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// save cursor, jump to the last log row, scroll up with \n, print, restore
	fmt.Printf("\0337\033[%d;1H\n%s\0338", s.rows-1, text)
	s.drawPromptLocked()
}

func (s *screen) drawPrompt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drawPromptLocked()
}

func (s *screen) drawPromptLocked() {
	fmt.Printf("\033[%d;1H\033[K> %s", s.rows, s.buf)
}

// inputLoop reads landmark names. Each non-empty submission calls onLabel;
// 'q'/'quit'/'exit', Ctrl-C, Ctrl-D or EOF call cancel and return.
func (s *screen) inputLoop(cancel func(), onLabel func(string)) {
	if s == nil {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if !dispatch(sc.Text(), cancel, onLabel) {
				return
			}
		}
		cancel() // stdin closed (Ctrl-D)
		return
	}
	r := bufio.NewReader(os.Stdin)
	for {
		b, err := r.ReadByte()
		if err != nil {
			cancel()
			return
		}
		switch {
		case b == 3 || b == 4: // Ctrl-C / Ctrl-D
			cancel()
			return
		case b == '\r' || b == '\n':
			s.mu.Lock()
			line := string(s.buf)
			s.buf = s.buf[:0]
			s.drawPromptLocked()
			s.mu.Unlock()
			if !dispatch(line, cancel, onLabel) {
				return
			}
		case b == 127 || b == 8: // backspace / DEL
			s.mu.Lock()
			if n := len(s.buf); n > 0 {
				s.buf = s.buf[:n-1] // ponytail: byte-wise, fine for ASCII landmarks
			}
			s.drawPromptLocked()
			s.mu.Unlock()
		case b == 27: // ESC: swallow a CSI sequence (arrow keys etc.)
			r.ReadByte()
			r.ReadByte()
		case b >= 32:
			s.mu.Lock()
			s.buf = append(s.buf, b)
			s.drawPromptLocked()
			s.mu.Unlock()
		}
	}
}

// dispatch acts on a submitted line. Returns false to stop the input loop.
func dispatch(line string, cancel func(), onLabel func(string)) bool {
	switch strings.TrimSpace(line) {
	case "q", "quit", "exit":
		cancel()
		return false
	case "":
		return true // bare Enter: ignore, don't clear the current label
	default:
		onLabel(strings.TrimSpace(line))
		return true
	}
}
