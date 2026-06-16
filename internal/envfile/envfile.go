// Package envfile parses .env files into key-value entries.
package envfile

import (
	"bufio"
	"io"
	"strings"
)

// Entry is a single key-value pair from a .env file.
type Entry struct {
	Key   string
	Value string
}

// Parse reads a .env file and returns entries in order.
// Supports: KEY=VALUE, KEY="VALUE", KEY='VALUE', export KEY=VALUE, # comments, blank lines.
// Lines without '=' are skipped. Duplicate keys: first occurrence wins.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // allow long single-line values (up to 10 MB)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip optional export prefix
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)

		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue // not a key=value line, skip
		}

		key := strings.TrimSpace(line[:idx])
		if key == "" || seen[key] {
			continue
		}

		value := line[idx+1:]

		// Quoted value: find the closing quote and extract content between them.
		// Anything after the closing quote (e.g. trailing comment) is ignored.
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			q := value[0]
			if end := strings.IndexByte(value[1:], q); end >= 0 {
				value = value[1 : end+1]
			} else {
				// No closing quote — treat as unquoted
				value = strings.TrimSpace(value)
			}
		} else {
			// Unquoted: strip inline comment (whitespace + # suffix) and trim
			if i := strings.IndexAny(value, " \t"); i >= 0 && strings.HasPrefix(strings.TrimSpace(value[i:]), "#") {
				value = value[:i]
			}
			value = strings.TrimSpace(value)
		}

		seen[key] = true
		entries = append(entries, Entry{Key: key, Value: value})
	}

	return entries, scanner.Err()
}
