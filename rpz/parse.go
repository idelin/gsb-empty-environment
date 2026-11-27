package rpz

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseZone compiles a BIND-style RPZ zone text. The origin is the zone apex
// and is used both as the default $ORIGIN and to recognize relative owners and
// the apex SOA/NS boilerplate. Only RRs relevant to RPZ policy are consumed;
// SOA/NS and the apex itself are ignored, while unsupported record types at a
// trigger owner produce an error.
func ParseZone(origin string, reader io.Reader) (*Zone, error) {
	zone := NewZone(origin)
	originLabels := SplitLabels(origin)

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		lineNo       int
		pending      string
		depth        int
		defaultTTL   uint32
		lastOwner    string
		leadingSpace bool
		rrBuf        = map[string][]Record{}
	)

	flush := func(owner string) error {
		records := rrBuf[owner]
		if records == nil {
			return nil
		}
		delete(rrBuf, owner)
		return zone.AddRule(owner, records...)
	}

	for scanner.Scan() {
		lineNo++
		rawLine := stripComment(scanner.Text())
		lineStartsRecord := strings.HasPrefix(rawLine, " ") || strings.HasPrefix(rawLine, "\t")
		// Determine whether this physical line starts a fresh owner before
		// updating the parenthesis depth. A line that opens a block (e.g.
		// "owner IN (") still starts its own record; subsequent folded lines
		// reuse that owner.
		opensRecord := depth == 0 && pending == "" && strings.TrimSpace(rawLine) != ""
		if opensRecord {
			leadingSpace = lineStartsRecord
		}
		depth += strings.Count(rawLine, "(") - strings.Count(rawLine, ")")
		if depth < 0 {
			return nil, fmt.Errorf("rpz: line %d: unmatched parenthesis", lineNo)
		}
		if strings.TrimSpace(rawLine) != "" {
			pending += " " + rawLine
		}
		if depth > 0 {
			continue
		}

		tokens := tokenize(pending)
		pending = ""
		if len(tokens) == 0 {
			continue
		}

		if strings.HasPrefix(tokens[0], "$") {
			switch strings.ToUpper(tokens[0]) {
			case "$ORIGIN":
				if len(tokens) != 2 {
					return nil, fmt.Errorf("rpz: line %d: malformed $ORIGIN", lineNo)
				}
				origin = qualify(tokens[1], origin)
				originLabels = SplitLabels(origin)
			case "$TTL":
				if len(tokens) != 2 {
					return nil, fmt.Errorf("rpz: line %d: malformed $TTL", lineNo)
				}
				ttl, err := parseTTL(tokens[1])
				if err != nil {
					return nil, fmt.Errorf("rpz: line %d: %w", lineNo, err)
				}
				defaultTTL = ttl
			case "$INCLUDE":
				return nil, fmt.Errorf("rpz: line %d: $INCLUDE is not supported", lineNo)
			default:
				return nil, fmt.Errorf("rpz: line %d: unsupported directive %s", lineNo, tokens[0])
			}
			continue
		}

		owner := lastOwner
		idx := 0
		if !leadingSpace {
			owner = qualify(tokens[0], origin)
			idx = 1
		}
		if owner == "" {
			return nil, fmt.Errorf("rpz: line %d: missing owner", lineNo)
		}

		// A folded logical line can contain several RRs sharing one owner;
		// split on RR type keywords and process each record independently.
		rrChunks, err := splitRecordChunks(tokens[idx:])
		if err != nil {
			return nil, fmt.Errorf("rpz: line %d: %w", lineNo, err)
		}
		for _, chunk := range rrChunks {
			ttl, typeIdx, err := scanTypeIndex(chunk, 0)
			if err != nil {
				return nil, fmt.Errorf("rpz: line %d: %w", lineNo, err)
			}
			if typeIdx >= len(chunk) {
				return nil, fmt.Errorf("rpz: line %d: missing record type", lineNo)
			}
			rrType := strings.ToUpper(chunk[typeIdx])
			rdata := chunk[typeIdx+1:]
			if ttl == 0 {
				ttl = defaultTTL
			}

			// The zone apex only carries boilerplate (SOA/NS); never treat it
			// as a QNAME rule.
			if equalLabels(SplitLabels(owner), originLabels) {
				continue
			}

			record, ok, err := buildRecord(rrType, rdata, ttl, origin)
			if err != nil {
				return nil, fmt.Errorf("rpz: line %d: %w", lineNo, err)
			}
			if ok {
				rrBuf[owner] = append(rrBuf[owner], record)
			}
		}

		// Owners appear contiguously; flush the previous owner on change.
		if lastOwner != "" && lastOwner != owner {
			if err := flush(lastOwner); err != nil {
				return nil, fmt.Errorf("rpz: line %d: %w", lineNo, err)
			}
		}
		lastOwner = owner
	}
	if depth != 0 {
		return nil, errors.New("rpz: unterminated parenthesized block")
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if pending != "" && len(tokenize(pending)) > 0 {
		return nil, errors.New("rpz: trailing incomplete record")
	}
	if err := flush(lastOwner); err != nil {
		return nil, err
	}
	return zone, nil
}

func buildRecord(rrType string, fields []string, ttl uint32, origin string) (Record, bool, error) {
	switch rrType {
	case "CNAME":
		if len(fields) != 1 {
			return Record{}, false, fmt.Errorf("CNAME expects one target, got %d", len(fields))
		}
		return Record{Type: "CNAME", Value: canonicalTarget(fields[0], origin), TTL: ttl}, true, nil
	case "A", "AAAA":
		if len(fields) != 1 {
			return Record{}, false, fmt.Errorf("%s expects one address", rrType)
		}
		return Record{Type: rrType, Value: fields[0], TTL: ttl}, true, nil
	case "TXT":
		if len(fields) == 0 {
			return Record{}, false, errors.New("TXT expects text")
		}
		return Record{Type: "TXT", Value: strings.Join(fields, " "), TTL: ttl}, true, nil
	case "SOA", "NS", "DNSKEY", "NSEC", "NSEC3", "RRSIG", "CDS", "CDNSKEY":
		return Record{}, false, nil
	default:
		return Record{}, false, fmt.Errorf("unsupported RPZ record type %q", rrType)
	}
}

// scanTypeIndex skips optional TTL / CLASS fields and returns the index of the
// RR type token together with the explicit TTL (zero when absent).
// splitRecordChunks breaks the owner-stripped token stream of one logical
// line into per-RR chunks. A new chunk starts at every recognized RR type
// keyword; optional TTL/CLASS tokens preceding a type stay attached to it.
func splitRecordChunks(tokens []string) ([][]string, error) {
	var chunks [][]string
	var current []string
	var meta []string
	flush := func() {
		if len(current) > 0 {
			chunks = append(chunks, current)
			current = nil
		}
	}
	for _, token := range tokens {
		if isRRType(token) {
			// Leading TTL/CLASS metadata belongs to this new RR.
			flush()
			current = append(current, meta...)
			meta = nil
			current = append(current, token)
			continue
		}
		if len(current) == 0 && (isTTL(token) || strings.EqualFold(token, "IN") || strings.EqualFold(token, "CLASS")) {
			meta = append(meta, token)
			continue
		}
		current = append(current, token)
	}
	flush()
	if len(chunks) == 0 {
		return nil, errors.New("no resource records found")
	}
	return chunks, nil
}

func isRRType(token string) bool {
	switch strings.ToUpper(token) {
	case "CNAME", "A", "AAAA", "TXT", "SOA", "NS", "DNSKEY", "NSEC", "NSEC3",
		"RRSIG", "CDS", "CDNSKEY", "MX", "PTR":
		return true
	default:
		return false
	}
}

func scanTypeIndex(tokens []string, start int) (uint32, int, error) {
	var ttl uint32
	for idx := start; idx < len(tokens); idx++ {
		token := tokens[idx]
		switch {
		case strings.EqualFold(token, "IN"):
			continue
		case strings.EqualFold(token, "CLASS"):
			if idx+1 >= len(tokens) {
				return 0, 0, errors.New("dangling CLASS")
			}
			idx++
			continue
		case isTTL(token):
			parsed, err := parseTTL(token)
			if err != nil {
				return 0, 0, err
			}
			ttl = parsed
		default:
			return ttl, idx, nil
		}
	}
	return ttl, len(tokens), nil
}

// canonicalTarget resolves the five RPZ special CNAME targets and qualifies
// ordinary relative targets against the current origin.
func canonicalTarget(target, origin string) string {
	switch target {
	case ".":
		return "."
	case "*":
		return "*."
	case "rpz-passthru", "rpz-passthru.":
		return "rpz-passthru."
	case "rpz-drop", "rpz-drop.":
		return "rpz-drop."
	case "rpz-tcp-only", "rpz-tcp-only.":
		return "rpz-tcp-only."
	}
	return qualify(target, origin)
}

func qualify(name, origin string) string {
	if name == "" || name == "@" {
		return origin
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "." + origin
}

func isTTL(token string) bool {
	if token == "" {
		return false
	}
	_, err := parseTTL(token)
	return err == nil
}

func parseTTL(token string) (uint32, error) {
	if seconds, err := strconv.ParseUint(token, 10, 32); err == nil {
		return uint32(seconds), nil
	}
	multipliers := map[byte]uint64{'s': 1, 'm': 60, 'h': 3600, 'd': 86400, 'w': 604800}
	var total uint64
	var num uint64
	haveNum := false
	for i := 0; i < len(token); i++ {
		ch := token[i]
		if ch >= '0' && ch <= '9' {
			num = num*10 + uint64(ch-'0')
			haveNum = true
			continue
		}
		mult, ok := multipliers[ch]
		if !ok || !haveNum {
			return 0, fmt.Errorf("invalid TTL %q", token)
		}
		total += num * mult
		num, haveNum = 0, false
	}
	if haveNum {
		total += num
	}
	if total == 0 || total > 1<<32-1 {
		return 0, fmt.Errorf("invalid TTL %q", token)
	}
	return uint32(total), nil
}

func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case ';':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

// tokenize splits on whitespace while keeping quoted strings together and
// dropping the parentheses used for line folding.
func tokenize(line string) []string {
	line = strings.ReplaceAll(line, "(", " ")
	line = strings.ReplaceAll(line, ")", " ")
	var tokens []string
	var builder strings.Builder
	inQuote := false
	flush := func() {
		if builder.Len() > 0 {
			tokens = append(tokens, builder.String())
			builder.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
			if inQuote {
				builder.WriteByte(ch)
			} else {
				flush()
			}
		default:
			builder.WriteByte(ch)
		}
	}
	flush()
	return tokens
}

func equalLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
