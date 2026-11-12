package taskpkg

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrInvalidGrid identifies malformed expanded-grid input.
	ErrInvalidGrid = errors.New("invalid grid")
	// ErrInvalidConfig identifies malformed binder configuration.
	ErrInvalidConfig = errors.New("invalid binder config")
	// ErrUnparsableHeader identifies an explicitly semantic header node whose
	// text (or configured value) does not yield the requested semantic value.
	ErrUnparsableHeader = errors.New("unparsable header")
)

// HeaderRole describes a semantic value carried by an explicitly configured
// header node. Roles are never inferred from arbitrary header text.
type HeaderRole string

const (
	HeaderRoleYear HeaderRole = "year"
	HeaderRoleUnit HeaderRole = "unit"
)

func (r HeaderRole) valid() bool {
	return r == HeaderRoleYear || r == HeaderRoleUnit
}

// HeaderSemantic marks one original header cell as the carrier of a year or
// unit. Source is matched against Cell.Source, so a spanned header applies to
// every column it covers. Value optionally supplies the semantic value
// directly; otherwise it is parsed from the header cell's text.
type HeaderSemantic struct {
	Source CellRef
	Role   HeaderRole
	Value  string
}

var yearPattern = regexp.MustCompile(`(?:19|20)\d{2}`)

// ParseYear extracts a single four-digit year from header text. Full-width
// digits and decorated labels such as "2024年" or "FY2024" are accepted
// through their ASCII year token.
func ParseYear(text string) (string, bool) {
	years := yearPattern.FindAllString(text, -1)
	if len(years) == 0 {
		return "", false
	}
	// A header node is expected to identify one year. If it contains a
	// range, use the first (left-most) year rather than inventing a value.
	return years[0], true
}

var (
	unitPrefixPattern  = regexp.MustCompile(`(?i)^(单位|unit)\s*[:：]?\s*`)
	bracketContentExpr = regexp.MustCompile(`[（(]\s*([^）)]+?)\s*[）)]`)
)

// ParseUnit extracts a unit label from explicitly configured header text.
// It understands common forms such as "单位：万元", "单位(万元)", "（万元）"
// and a bare "万元". It deliberately does not infer units from unconfigured
// header text.
func ParseUnit(text string) (string, bool) {
	value := strings.TrimSpace(text)
	if value == "" {
		return "", false
	}

	if matches := bracketContentExpr.FindAllStringSubmatch(value, -1); len(matches) > 0 {
		// Use the last bracket group; labels sometimes wrap the actual unit.
		value = strings.TrimSpace(matches[len(matches)-1][1])
	} else {
		value = strings.TrimSpace(unitPrefixPattern.ReplaceAllString(value, ""))
	}
	// Labels such as "（单位：万元）" put the prefix inside the bracket.
	value = strings.TrimSpace(unitPrefixPattern.ReplaceAllString(value, ""))

	value = strings.TrimSpace(value)
	value = strings.Trim(value, " \t\r\n:：")
	if value == "" {
		return "", false
	}
	return value, true
}

type semanticIndex struct {
	values map[string]map[HeaderRole]HeaderSemantic
}

func buildSemanticIndex(semantics []HeaderSemantic) (semanticIndex, error) {
	index := semanticIndex{
		values: make(map[string]map[HeaderRole]HeaderSemantic, len(semantics)),
	}
	for i, semantic := range semantics {
		if !semantic.Role.valid() {
			return semanticIndex{}, fmt.Errorf("%w: header semantics %d has role %q",
				ErrInvalidConfig, i, semantic.Role)
		}
		key := semantic.Source.Key()
		roles := index.values[key]
		if roles == nil {
			roles = make(map[HeaderRole]HeaderSemantic)
			index.values[key] = roles
		}
		if existing, ok := roles[semantic.Role]; ok {
			return semanticIndex{}, fmt.Errorf("%w: duplicate %s semantics for source %s (rows %d and %d)",
				ErrInvalidConfig, semantic.Role, key, existing.Source.Row, semantic.Source.Row)
		}
		roles[semantic.Role] = semantic
	}
	return index, nil
}

// lookup returns the configured semantic role for an original source cell.
// IDs take precedence, with coordinates as a fallback so configuration can
// address cells independently of the host's ID scheme.
func (index semanticIndex) lookup(source CellRef, role HeaderRole) (HeaderSemantic, bool) {
	if roles, ok := index.values[source.Key()]; ok {
		if semantic, found := roles[role]; found {
			return semantic, true
		}
	}
	for _, roles := range index.values {
		if semantic, found := roles[role]; found && semantic.Source.Matches(source) {
			return semantic, true
		}
	}
	return HeaderSemantic{}, false
}

func semanticValue(semantic HeaderSemantic, node HeaderNode) (string, error) {
	if strings.TrimSpace(semantic.Value) != "" {
		return strings.TrimSpace(semantic.Value), nil
	}

	switch semantic.Role {
	case HeaderRoleYear:
		if year, ok := ParseYear(node.Text); ok {
			return year, nil
		}
	case HeaderRoleUnit:
		if unit, ok := ParseUnit(node.Text); ok {
			return unit, nil
		}
	default:
		return "", fmt.Errorf("%w: unsupported role %q", ErrInvalidConfig, semantic.Role)
	}

	return "", fmt.Errorf("%w: cannot parse %s from header %q at row %d (source %s)",
		ErrUnparsableHeader, semantic.Role, node.Text, node.Level, node.Source.Key())
}
