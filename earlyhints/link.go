package earlyhints

import (
	"net/url"
	"sort"
	"strings"
)

// LinkValue is a single parsed RFC 8288 Link header value.
type LinkValue struct {
	// Target is the URI reference inside the angle brackets, e.g. "/app.js".
	Target string
	// Params holds the link parameters (semicolon separated). Parameter names
	// are lower-cased; values retain their original, quote-stripped form.
	Params map[string]string
	// Raw is the original textual representation of this link value.
	Raw string
}

// ParseLinkHeader parses one or more RFC 8288 Link header field values.
// Multiple Link headers and comma-separated values within a single header
// are all flattened into one slice. Malformed values are silently dropped.
func ParseLinkHeader(values []string) []LinkValue {
	var links []LinkValue
	for _, header := range values {
		for _, segment := range splitLinkValues(header) {
			link, ok := parseLinkValue(segment)
			if ok {
				links = append(links, link)
			}
		}
	}
	return links
}

// FilterLinkValues keeps only links that are safe to forward as early hints:
// the relation must be preload or preconnect, the target must be an absolute
// https URL whose host is contained in allowedHosts. The returned strings are
// normalized single link values suitable for use in a Link header.
func FilterLinkValues(links []LinkValue, allowedHosts map[string]bool) []string {
	kept := make([]string, 0, len(links))
	for _, link := range links {
		if !isPreloadRelation(link.Params["rel"]) {
			continue
		}
		target, err := url.Parse(link.Target)
		if err != nil || !target.IsAbs() || !strings.EqualFold(target.Scheme, "https") {
			continue
		}
		host := strings.ToLower(target.Hostname())
		if host == "" || !allowedHosts[host] {
			continue
		}
		kept = append(kept, serializeLink(link))
	}
	return kept
}

// isPreloadRelation reports whether the rel parameter contains one of the
// allowed relation types. rel is a whitespace-separated, case-insensitive
// list of relation types.
func isPreloadRelation(rel string) bool {
	for _, token := range strings.Fields(rel) {
		switch strings.ToLower(token) {
		case "preload", "preconnect":
			return true
		}
	}
	return false
}

// splitLinkValues splits a Link header on commas that separate link values.
// Commas inside angle brackets or double-quoted strings are not separators.
func splitLinkValues(header string) []string {
	var values []string
	var current strings.Builder
	inAngle := false
	inQuote := false
	flush := func() {
		if strings.TrimSpace(current.String()) != "" {
			values = append(values, strings.TrimSpace(current.String()))
		}
		current.Reset()
	}
	for i := 0; i < len(header); i++ {
		ch := header[i]
		switch ch {
		case '<':
			if !inQuote {
				inAngle = true
			}
		case '>':
			if !inQuote {
				inAngle = false
			}
		case '"':
			if !inAngle {
				inQuote = !inQuote
			}
		case ',':
			if !inAngle && !inQuote {
				flush()
				continue
			}
		case '\\':
			if inQuote && i+1 < len(header) {
				current.WriteByte(ch)
				i++
				ch = header[i]
			}
		}
		current.WriteByte(ch)
	}
	flush()
	return values
}

func parseLinkValue(raw string) (LinkValue, bool) {
	trimmed := strings.TrimSpace(raw)
	open := strings.IndexByte(trimmed, '<')
	if open < 0 {
		return LinkValue{}, false
	}
	closeBracket := strings.IndexByte(trimmed[open:], '>')
	if closeBracket < 0 {
		return LinkValue{}, false
	}
	closeBracket += open
	target := trimmed[open+1 : closeBracket]
	if target == "" {
		return LinkValue{}, false
	}
	params := parseLinkParams(trimmed[closeBracket+1:])
	return LinkValue{Target: target, Params: params, Raw: trimmed}, true
}

func parseLinkParams(rest string) map[string]string {
	params := map[string]string{}
	for _, part := range splitSemicolons(rest) {
		name, value, found := strings.Cut(part, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !found {
			params[name] = ""
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = unquoteLinkValue(value[1 : len(value)-1])
		}
		params[name] = value
	}
	return params
}

// splitSemicolons splits parameter lists on semicolons that are not inside a
// double-quoted string.
func splitSemicolons(input string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	flush := func() {
		if strings.TrimSpace(current.String()) != "" {
			parts = append(parts, strings.TrimSpace(current.String()))
		}
		current.Reset()
	}
	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch ch {
		case '"':
			inQuote = !inQuote
		case ';':
			if !inQuote {
				flush()
				continue
			}
		case '\\':
			if inQuote && i+1 < len(input) {
				current.WriteByte(ch)
				i++
				ch = input[i]
			}
		}
		current.WriteByte(ch)
	}
	flush()
	return parts
}

func unquoteLinkValue(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
		}
		builder.WriteByte(value[i])
	}
	return builder.String()
}

// serializeLink rebuilds a link value, lower-casing the rel parameter while
// preserving every other parameter. This guarantees only parser-accepted
// syntax reaches the client.
func serializeLink(link LinkValue) string {
	var builder strings.Builder
	builder.WriteByte('<')
	builder.WriteString(link.Target)
	builder.WriteByte('>')
	if rel, ok := link.Params["rel"]; ok {
		writeLinkParam(&builder, "rel", strings.ToLower(rel))
	}
	for _, name := range sortedParamNames(link.Params) {
		if name == "rel" {
			continue
		}
		writeLinkParam(&builder, name, link.Params[name])
	}
	return builder.String()
}

func writeLinkParam(builder *strings.Builder, name, value string) {
	builder.WriteString("; ")
	builder.WriteString(name)
	if value != "" {
		builder.WriteString("=")
		builder.WriteString(quoteParamIfNeeded(value))
	}
}

func sortedParamNames(params map[string]string) []string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func quoteParamIfNeeded(value string) string {
	if strings.ContainsAny(value, " \t;,\"") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}
