package cmd

import (
	"strings"
	"testing"
)

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
