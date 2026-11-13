package netsec

import (
	"strconv"
	"strings"
)

// XrefEntry is one group/article-number location from an Xref header.
type XrefEntry struct {
	Group  string
	Number string
}

// Xref is the parsed form of an Xref header value.
type Xref struct {
	Server  string
	Entries []XrefEntry
}

// ParseXref parses an Xref header value (with or without the "Xref:" header
// name). It is deliberately lenient about folding and whitespace, but only
// accepts entries whose article number is numeric.
func ParseXref(raw string) Xref {
	raw = strings.TrimSpace(unfoldHeader(raw))
	if raw == "" {
		return Xref{}
	}

	if i := strings.IndexByte(raw, ':'); i >= 0 && strings.EqualFold(strings.TrimSpace(raw[:i]), "xref") {
		raw = strings.TrimSpace(raw[i+1:])
	}

	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return Xref{}
	}

	var out Xref
	start := 0
	if _, ok := parseXrefEntry(fields[0]); ok {
		// Some archives omit the server field. Treat the first token as a
		// location rather than silently dropping it.
		start = 0
	} else {
		out.Server = fields[0]
		start = 1
	}

	for _, field := range fields[start:] {
		entry, ok := parseXrefEntry(field)
		if ok {
			out.Entries = append(out.Entries, entry)
		}
	}
	return out
}

func parseXrefEntry(field string) (XrefEntry, bool) {
	i := strings.LastIndexByte(field, ':')
	if i <= 0 || i == len(field)-1 {
		return XrefEntry{}, false
	}

	group := strings.TrimSpace(field[:i])
	number := strings.TrimSpace(field[i+1:])
	if group == "" || number == "" {
		return XrefEntry{}, false
	}
	if _, err := strconv.ParseUint(number, 10, 64); err != nil {
		return XrefEntry{}, false
	}
	return XrefEntry{Group: group, Number: number}, true
}

func unfoldHeader(v string) string {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	if !strings.Contains(v, "\n") {
		return v
	}
	lines := strings.Split(v, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

// String renders an Xref value without the header name.
func (x Xref) String() string {
	parts := make([]string, 0, len(x.Entries)+1)
	if x.Server != "" {
		parts = append(parts, x.Server)
	}
	for _, entry := range x.Entries {
		parts = append(parts, entry.Group+":"+entry.Number)
	}
	return strings.Join(parts, " ")
}

// Groups returns the groups named by the Xref header.
func (x Xref) Groups() []string {
	groups := make([]string, 0, len(x.Entries))
	for _, entry := range x.Entries {
		groups = append(groups, entry.Group)
	}
	return groups
}
