package prompt

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func newTestPrompter(input string) (*Prompter, *bytes.Buffer) {
	var w bytes.Buffer
	return New(strings.NewReader(input), &w), &w
}

// A non-TTY reader (a string) makes Secret fall back to a normal line read,
// which is the path tests can exercise.
func TestSecret_NonTTYReadsLine(t *testing.T) {
	p, w := newTestPrompter("my-secret\n")
	got, err := p.Secret("Value: ")
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if got != "my-secret" {
		t.Fatalf("got %q, want %q", got, "my-secret")
	}
	if !strings.Contains(w.String(), "Value: ") {
		t.Fatalf("prompt not written: %q", w.String())
	}
}

func TestLine_PreservesInnerSpaces(t *testing.T) {
	p, _ := newTestPrompter("a b c\n")
	got, err := p.Line("> ")
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if got != "a b c" {
		t.Fatalf("got %q, want %q", got, "a b c")
	}
}

func TestLine_CarriageReturn(t *testing.T) {
	p, _ := newTestPrompter("secret\r\n")
	got, err := p.Line("> ")
	if err != nil || got != "secret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestMultipleReads(t *testing.T) {
	p, _ := newTestPrompter("first\nsecond\nthird\n")
	v1, _ := p.Line("1: ")
	v2, _ := p.Secret("2: ")
	v3, _ := p.Line("3: ")
	if v1 != "first" || v2 != "second" || v3 != "third" {
		t.Fatalf("got %q/%q/%q, want first/second/third", v1, v2, v3)
	}
}

func TestLine_EOF(t *testing.T) {
	p, _ := newTestPrompter("")
	if _, err := p.Line("> "); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
