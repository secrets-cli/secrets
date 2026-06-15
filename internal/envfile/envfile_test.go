package envfile

import (
	"strings"
	"testing"
)

func parse(t *testing.T, input string) []Entry {
	t.Helper()
	entries, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return entries
}

func TestParse_LongValue(t *testing.T) {
	big := strings.Repeat("x", 200*1024) // exceeds bufio.Scanner's 64 KB default token cap
	got := parse(t, "BIG="+big+"\n")
	if len(got) != 1 || got[0].Value != big {
		t.Fatalf("long single-line value not parsed whole (entries=%d, value len=%d)", len(got), len(got[0].Value))
	}
}

func TestBasic(t *testing.T) {
	entries := parse(t, "KEY=value\n")
	if len(entries) != 1 || entries[0].Key != "KEY" || entries[0].Value != "value" {
		t.Fatalf("unexpected: %+v", entries)
	}
}

func TestDoubleQuotes(t *testing.T) {
	entries := parse(t, `KEY="hello world"`)
	if entries[0].Value != "hello world" {
		t.Fatalf("got %q", entries[0].Value)
	}
}

func TestSingleQuotes(t *testing.T) {
	entries := parse(t, `KEY='hello world'`)
	if entries[0].Value != "hello world" {
		t.Fatalf("got %q", entries[0].Value)
	}
}

func TestExportPrefix(t *testing.T) {
	entries := parse(t, "export KEY=value\n")
	if len(entries) != 1 || entries[0].Key != "KEY" {
		t.Fatalf("unexpected: %+v", entries)
	}
}

func TestComments(t *testing.T) {
	entries := parse(t, "# comment\nKEY=value\n# another\n")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestBlankLines(t *testing.T) {
	entries := parse(t, "\nKEY=value\n\n")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestEmptyValue(t *testing.T) {
	entries := parse(t, "KEY=\n")
	if len(entries) != 1 || entries[0].Value != "" {
		t.Fatalf("unexpected: %+v", entries)
	}
}

func TestNoEqualsSkipped(t *testing.T) {
	entries := parse(t, "NOEQUALS\nKEY=value\n")
	if len(entries) != 1 || entries[0].Key != "KEY" {
		t.Fatalf("unexpected: %+v", entries)
	}
}

func TestDuplicateFirstWins(t *testing.T) {
	entries := parse(t, "KEY=first\nKEY=second\n")
	if len(entries) != 1 || entries[0].Value != "first" {
		t.Fatalf("unexpected: %+v", entries)
	}
}

func TestOrderPreserved(t *testing.T) {
	entries := parse(t, "ZEBRA=z\nALPHA=a\nMIKE=m\n")
	if entries[0].Key != "ZEBRA" || entries[1].Key != "ALPHA" || entries[2].Key != "MIKE" {
		t.Fatalf("order not preserved: %+v", entries)
	}
}

func TestMixedContent(t *testing.T) {
	input := `
# Database
export DB_URL="postgres://localhost/mydb"
API_KEY='abc123'
EMPTY=
# trailing comment
`
	entries := parse(t, input)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Key != "DB_URL" || entries[0].Value != "postgres://localhost/mydb" {
		t.Fatalf("DB_URL: %+v", entries[0])
	}
	if entries[1].Key != "API_KEY" || entries[1].Value != "abc123" {
		t.Fatalf("API_KEY: %+v", entries[1])
	}
	if entries[2].Key != "EMPTY" || entries[2].Value != "" {
		t.Fatalf("EMPTY: %+v", entries[2])
	}
}

func TestInlineComment(t *testing.T) {
	entries := parse(t, "KEY=123  # comment\n")
	if entries[0].Value != "123" {
		t.Fatalf("inline comment not stripped, got %q", entries[0].Value)
	}
}

func TestInlineComment2(t *testing.T) {
	entries := parse(t, "KEY='ABC'  # comment\n")
	if entries[0].Value != "ABC" {
		t.Fatalf("inline comment not stripped, got %q", entries[0].Value)
	}
}

func TestInlineComment3(t *testing.T) {
	entries := parse(t, `KEY="ABC"  # comment\n`)
	if entries[0].Value != "ABC" {
		t.Fatalf("inline comment not stripped, got %q", entries[0].Value)
	}
}

func TestInlineCommentTab(t *testing.T) {
	entries := parse(t, "KEY=123\t# comment\n")
	if entries[0].Value != "123" {
		t.Fatalf("tab inline comment not stripped, got %q", entries[0].Value)
	}
}

func TestInlineCommentNoSpace(t *testing.T) {
	// No whitespace before # — not a comment, part of the value
	entries := parse(t, "KEY=123#nospace\n")
	if entries[0].Value != "123#nospace" {
		t.Fatalf("# without preceding whitespace should not be stripped, got %q", entries[0].Value)
	}
}

func TestInlineCommentInsideQuotes(t *testing.T) {
	// Quoted value: # inside quotes is preserved
	entries := parse(t, `KEY="123 # not a comment"`)
	if entries[0].Value != "123 # not a comment" {
		t.Fatalf("# inside quotes should be preserved, got %q", entries[0].Value)
	}
}

func TestUnmatchedQuotesNotStripped(t *testing.T) {
	entries := parse(t, `KEY="value'`)
	if entries[0].Value != `"value'` {
		t.Fatalf("mismatched quotes should not be stripped, got %q", entries[0].Value)
	}
}
