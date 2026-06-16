package format

import (
	"testing"
)

// testCase defines an input value and expected outputs for each format.
type testCase struct {
	name   string
	value  string
	posix  string
	fish   string
	dotenv string
}

var cases = []testCase{
	{
		name:   "simple",
		value:  "hello",
		posix:  "export KEY='hello'",
		fish:   "set -x KEY 'hello'",
		dotenv: "KEY=hello",
	},
	{
		name:   "empty",
		value:  "",
		posix:  "export KEY=''",
		fish:   "set -x KEY ''",
		dotenv: "KEY=",
	},
	{
		name:   "spaces",
		value:  "hello world",
		posix:  "export KEY='hello world'",
		fish:   "set -x KEY 'hello world'",
		dotenv: "KEY=hello world",
	},
	{
		name:   "single_quotes",
		value:  "it's here",
		posix:  `export KEY='it'\''s here'`,
		fish:   `set -x KEY 'it\'s here'`,
		dotenv: "KEY=it's here",
	},
	{
		name:   "double_quotes",
		value:  `say "hi"`,
		posix:  `export KEY='say "hi"'`,
		fish:   `set -x KEY 'say "hi"'`,
		dotenv: `KEY=say "hi"`,
	},
	{
		name:   "backslash",
		value:  `path\to\file`,
		posix:  `export KEY='path\to\file'`,
		fish:   `set -x KEY 'path\\to\\file'`,
		dotenv: `KEY=path\to\file`,
	},
	{
		name:   "dollar_sign",
		value:  "$HOME/bin",
		posix:  "export KEY='$HOME/bin'",
		fish:   "set -x KEY '$HOME/bin'",
		dotenv: "KEY=$HOME/bin",
	},
	{
		name:   "backtick",
		value:  "`whoami`",
		posix:  "export KEY='`whoami`'",
		fish:   "set -x KEY '`whoami`'",
		dotenv: "KEY=`whoami`",
	},
	{
		name:   "newline",
		value:  "line1\nline2",
		posix:  "export KEY='line1\nline2'",
		fish:   "set -x KEY 'line1\nline2'",
		dotenv: "KEY=line1\nline2",
	},
	{
		name:   "tab",
		value:  "col1\tcol2",
		posix:  "export KEY='col1\tcol2'",
		fish:   "set -x KEY 'col1\tcol2'",
		dotenv: "KEY=col1\tcol2",
	},
	{
		name:   "special_chars",
		value:  "!@#$%^&*(){}[]|;:<>,?/~",
		posix:  "export KEY='!@#$%^&*(){}[]|;:<>,?/~'",
		fish:   "set -x KEY '!@#$%^&*(){}[]|;:<>,?/~'",
		dotenv: "KEY=!@#$%^&*(){}[]|;:<>,?/~",
	},
	{
		name:   "unicode",
		value:  "\u00e9\u00e8\u00ea \u00fc\u00f6\u00e4",
		posix:  "export KEY='\u00e9\u00e8\u00ea \u00fc\u00f6\u00e4'",
		fish:   "set -x KEY '\u00e9\u00e8\u00ea \u00fc\u00f6\u00e4'",
		dotenv: "KEY=\u00e9\u00e8\u00ea \u00fc\u00f6\u00e4",
	},
	{
		name:   "equals_sign",
		value:  "key=value=extra",
		posix:  "export KEY='key=value=extra'",
		fish:   "set -x KEY 'key=value=extra'",
		dotenv: "KEY=key=value=extra",
	},
	{
		name:   "url_with_credentials",
		value:  "https://user:pass@host:8080/path?q=1&r=2#frag",
		posix:  "export KEY='https://user:pass@host:8080/path?q=1&r=2#frag'",
		fish:   "set -x KEY 'https://user:pass@host:8080/path?q=1&r=2#frag'",
		dotenv: "KEY=https://user:pass@host:8080/path?q=1&r=2#frag",
	},
	{
		name:   "hex_private_key",
		value:  "0xdeadbeef0123456789abcdef",
		posix:  "export KEY='0xdeadbeef0123456789abcdef'",
		fish:   "set -x KEY '0xdeadbeef0123456789abcdef'",
		dotenv: "KEY=0xdeadbeef0123456789abcdef",
	},
	{
		name:   "both_quotes",
		value:  `it's a "test"`,
		posix:  `export KEY='it'\''s a "test"'`,
		fish:   `set -x KEY 'it\'s a "test"'`,
		dotenv: `KEY=it's a "test"`,
	},
	{
		name:   "carriage_return",
		value:  "line1\r\nline2",
		posix:  "export KEY='line1\r\nline2'",
		fish:   "set -x KEY 'line1\r\nline2'",
		dotenv: "KEY=line1\r\nline2",
	},
	{
		name:   "multiple_single_quotes",
		value:  "a'b'c'd",
		posix:  `export KEY='a'\''b'\''c'\''d'`,
		fish:   `set -x KEY 'a\'b\'c\'d'`,
		dotenv: "KEY=a'b'c'd",
	},
	{
		name:   "backslash_and_single_quote",
		value:  `\' combo`,
		posix:  `export KEY='\'\'' combo'`,
		fish:   `set -x KEY '\\\' combo'`,
		dotenv: `KEY=\' combo`,
	},
}

func TestPosix(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Posix("KEY", tc.value)
			if got != tc.posix {
				t.Errorf("Posix(%q)\n  got:  %s\n  want: %s", tc.value, got, tc.posix)
			}
		})
	}
}

func TestFish(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Fish("KEY", tc.value)
			if got != tc.fish {
				t.Errorf("Fish(%q)\n  got:  %s\n  want: %s", tc.value, got, tc.fish)
			}
		})
	}
}

func TestDotenv(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Dotenv("KEY", tc.value)
			if got != tc.dotenv {
				t.Errorf("Dotenv(%q)\n  got:  %s\n  want: %s", tc.value, got, tc.dotenv)
			}
		})
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"RPC_URL", "_x", "A1", "lower_case", "ALL_CAPS_99"}
	invalid := []string{
		"", "2FA", "prod/KEY", "FOO BAR", "a-b", "KEY=v",
		"EVIL$(touch x)", "X;rm -rf ~", "a\nb", "café",
	}
	for _, s := range valid {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false (would be unsafe to eval)", s)
		}
	}
}

func TestHasNewline(t *testing.T) {
	for _, s := range []string{"a\nb", "a\r\nb", "trailing\n", "\r"} {
		if !HasNewline(s) {
			t.Errorf("HasNewline(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "single line", "no\tnewline here"} {
		if HasNewline(s) {
			t.Errorf("HasNewline(%q) = true, want false", s)
		}
	}
}
