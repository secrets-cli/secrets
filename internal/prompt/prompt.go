// Package prompt provides simple interactive terminal prompts over a shared
// buffered reader, so sequential reads work correctly.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Prompter handles interactive prompts with a shared buffered reader.
type Prompter struct {
	r     *bufio.Reader
	w     io.Writer
	fd    int
	isTTY bool
}

// New creates a Prompter from a reader and writer.
// For testing, pass strings.NewReader / bytes.Buffer.
func New(r io.Reader, w io.Writer) *Prompter {
	p := &Prompter{r: bufio.NewReader(r), w: w}
	if f, ok := r.(*os.File); ok {
		p.fd = int(f.Fd())
		p.isTTY = term.IsTerminal(p.fd)
	}
	return p
}

// Secret prompts for a value, masking each character with '*' as it's typed —
// for entering secrets. On a non-TTY (piped) reader it falls back to a normal
// line read. The value is returned exactly as entered (no trimming), since a
// secret may legitimately contain surrounding whitespace.
func (p *Prompter) Secret(msg string) (string, error) {
	fmt.Fprint(p.w, msg)
	if !p.isTTY {
		return p.readLine()
	}
	s, err := readMasked(p.fd, p.w)
	fmt.Fprintln(p.w) // the user's Enter isn't echoed in raw mode
	if err != nil {
		return "", err
	}
	return s, nil
}

// Line prompts for a line of visible text input.
func (p *Prompter) Line(msg string) (string, error) {
	fmt.Fprint(p.w, msg)
	return p.readLine()
}

func (p *Prompter) readLine() (string, error) {
	line, err := p.r.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readMasked reads from fd in raw mode, echoing '*' for each printable
// character. Backspace deletes the last one. Returns on Enter or EOF.
func readMasked(fd int, w io.Writer) (string, error) {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	tty := os.NewFile(uintptr(fd), "/dev/tty")
	var buf []byte
	b := make([]byte, 1)
	for {
		if _, err := tty.Read(b); err != nil {
			return "", err
		}
		switch b[0] {
		case '\r', '\n':
			return string(buf), nil
		case 3: // Ctrl+C
			return "", fmt.Errorf("interrupted")
		case 4: // Ctrl+D (EOF)
			return string(buf), nil
		case 127, 8: // backspace / delete
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Fprint(w, "\b \b")
			}
		default:
			if b[0] >= 32 { // printable only
				buf = append(buf, b[0])
				fmt.Fprint(w, "*")
			}
		}
	}
}
