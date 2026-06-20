package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/vars-cli/vars/internal/prompt"
)

func TestResolveConflict(t *testing.T) {
	cases := []struct {
		name       string
		input      string // what the user "types" (newline-terminated answers)
		wantAction conflictAction
		wantKey    string
	}{
		{"replace short", "r\n", actionReplace, ""},
		{"replace word", "Replace\n", actionReplace, ""},
		{"skip explicit", "s\n", actionSkip, ""},
		{"skip on unrecognized", "huh?\n", actionSkip, ""},
		{"skip on empty", "\n", actionSkip, ""},
		{"rename to free key", "n\ndev/K\n", actionRename, "dev/K"},
		{"rename trims spaces", "n\n  prod/K  \n", actionRename, "prod/K"},
		{"rename then empty name is a skip", "n\n\n", actionSkip, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := prompt.New(strings.NewReader(tc.input), io.Discard)
			action, key, err := resolveConflict(p)
			if err != nil {
				t.Fatalf("resolveConflict: %v", err)
			}
			if action != tc.wantAction || key != tc.wantKey {
				t.Fatalf("got (%d, %q), want (%d, %q)", action, key, tc.wantAction, tc.wantKey)
			}
		})
	}
}

func TestPreview(t *testing.T) {
	long := preview("0xdeadbeefcafebabe") // 18 chars
	if !strings.HasPrefix(long, "0xdead") {
		t.Fatalf("preview should keep a short prefix: %q", long)
	}
	if strings.Contains(long, "cafebabe") {
		t.Fatalf("preview leaked the tail of the secret: %q", long)
	}
	if !strings.Contains(long, "18 chars") {
		t.Fatalf("preview should report length: %q", long)
	}
	// A short value must not be revealed at all.
	if got := preview("pin1"); strings.Contains(got, "pin1") {
		t.Fatalf("preview revealed a short secret: %q", got)
	}
}

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order not preserved: got %v, want %v", got, want)
		}
	}
}
