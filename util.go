package commonplace

import (
	"encoding/json"
	"strings"
	"time"
)

func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

func parseTags(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ftsQuery turns a free-text query into a safe FTS5 MATCH expression: each
// alphanumeric token becomes a quoted-OR term. Avoids FTS5 syntax errors
// from punctuation in user queries and gives reasonable keyword recall.
func ftsQuery(q string) string {
	var terms []string
	for _, f := range strings.Fields(q) {
		var b strings.Builder
		for _, r := range f {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			terms = append(terms, `"`+b.String()+`"`)
		}
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " OR ")
}
