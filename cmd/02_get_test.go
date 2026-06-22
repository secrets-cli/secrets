package cmd

import "testing"

func TestParseVersionRef(t *testing.T) {
	cases := []struct {
		in      string
		base    string
		n       int
		isRef   bool
		wantErr bool
	}{
		{"RPC_URL", "RPC_URL", 0, false, false},
		{"RPC_URL~1", "RPC_URL", 1, true, false},
		{"prod/KEY~2", "prod/KEY", 2, true, false},
		{"RPC_URL~0", "RPC_URL", 0, true, false},
		{"RPC_URL~", "", 0, true, true},
		{"RPC_URL~abc", "", 0, true, true},
		{"~2", "", 0, true, true},
		{"RPC_URL~-1", "", 0, true, true},
	}
	for _, c := range cases {
		base, n, isRef, err := parseVersionRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if base != c.base || n != c.n || isRef != c.isRef {
			t.Errorf("%q: got (%q,%d,%v), want (%q,%d,%v)", c.in, base, n, isRef, c.base, c.n, c.isRef)
		}
	}
}
