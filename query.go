package netsec

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	// DefaultPageSize is used when the request does not supply _count.
	DefaultPageSize = 100

	// MaxPageSize is the upper bound accepted for _count.
	MaxPageSize = 1000

	maxParsedChainHops = 64
	maxIncludeSpecs    = 64
)

// ChainStep is one reference hop in a chained search parameter.
type ChainStep struct {
	Param      string
	TargetType string
}

// Condition is a single search predicate. A condition with Chain steps is
// evaluated against the resources found by following the chain, never by
// querying the store with an unbounded join.
type Condition struct {
	Param    string
	Modifier string
	Values   []string
	Chain    []ChainStep
}

// ChainDepth returns the number of reference hops in the condition.
func (c Condition) ChainDepth() int {
	return len(c.Chain)
}

// IncludeSpec describes an _include or _revinclude directive.
type IncludeSpec struct {
	SourceType  string
	Param       string
	TargetTypes []string
	Iterate     bool
	Reverse     bool
}

func (s IncludeSpec) key() string {
	direction := "include"
	if s.Reverse {
		direction = "revinclude"
	}
	return direction + "\x00" + s.SourceType + "\x00" + s.Param + "\x00" +
		strings.Join(s.TargetTypes, ",") + "\x00" + strconv.FormatBool(s.Iterate)
}

func (s IncludeSpec) allowsForward(source *Resource) bool {
	if source == nil {
		return false
	}
	return s.SourceType == "*" || s.SourceType == source.Type
}

func (s IncludeSpec) allowsReverseTarget(target *Resource) bool {
	if target == nil {
		return false
	}
	if len(s.TargetTypes) == 0 {
		return true
	}
	for _, typ := range s.TargetTypes {
		if typ == "*" || typ == target.Type {
			return true
		}
	}
	return false
}

func (s IncludeSpec) allowsForwardTarget(target *Resource) bool {
	if target == nil {
		return false
	}
	if len(s.TargetTypes) == 0 {
		return true
	}
	for _, typ := range s.TargetTypes {
		if typ == "*" || typ == target.Type {
			return true
		}
	}
	return false
}

// SearchQuery is the parsed, authorization-neutral form of a search request.
// Parsing never decides who may read a resource; Engine applies the access
// scope to the base set and to every reference hop.
type SearchQuery struct {
	ResourceType    string
	Conditions      []Condition
	Includes        []IncludeSpec
	ReverseIncludes []IncludeSpec
	Offset          int
	Limit           int
	CountOnly       bool
	limitSet        bool
}

// Query is an alias for SearchQuery.
type Query = SearchQuery

// ParseSearch parses FHIR-style url.Values for resourceType.
func ParseSearch(resourceType string, params url.Values) (*SearchQuery, error) {
	resourceType = strings.TrimSpace(resourceType)
	if !validResourceType(resourceType) {
		return nil, fmt.Errorf("%w: invalid resource type %q", ErrInvalidQuery, resourceType)
	}

	query := &SearchQuery{ResourceType: resourceType, Limit: DefaultPageSize}
	seenIncludes := make(map[string]struct{})
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	addInclude := func(spec IncludeSpec) {
		if len(seenIncludes) >= maxIncludeSpecs {
			return
		}
		if _, ok := seenIncludes[spec.key()]; ok {
			return
		}
		seenIncludes[spec.key()] = struct{}{}
		if spec.Reverse {
			query.ReverseIncludes = append(query.ReverseIncludes, spec)
		} else {
			query.Includes = append(query.Includes, spec)
		}
	}

	for _, key := range keys {
		values := params[key]
		switch {
		case strings.HasPrefix(key, "_include") || strings.HasPrefix(key, "_revinclude"):
			specs, err := parseIncludeKey(key, values, resourceType)
			if err != nil {
				return nil, err
			}
			for _, spec := range specs {
				addInclude(spec)
			}
		case key == "_count":
			raw, err := singleValue(key, values)
			if err != nil {
				return nil, err
			}
			count, err := parseNonNegative(key, raw)
			if err != nil {
				return nil, err
			}
			if count == 0 {
				query.CountOnly = true
				query.Limit = 0
			} else if count > MaxPageSize {
				query.Limit = MaxPageSize
			} else {
				query.Limit = count
			}
			query.limitSet = true
		case key == "_offset":
			raw, err := singleValue(key, values)
			if err != nil {
				return nil, err
			}
			offset, err := parseNonNegative(key, raw)
			if err != nil {
				return nil, err
			}
			query.Offset = offset
		case key == "_summary":
			raw, err := singleValue(key, values)
			if err != nil {
				return nil, err
			}
			switch strings.ToLower(raw) {
			case "count":
				query.CountOnly = true
			case "", "false", "true", "text", "data":
			default:
				return nil, fmt.Errorf("%w: unsupported _summary value %q", ErrUnsupported, raw)
			}
		case key == "_total" || key == "_sort" || key == "_elements" ||
			key == "_format" || key == "_pretty" || key == "_prettyPrint":
			// These affect presentation only. Sorting is always applied by
			// Engine so paging remains stable and scope-independent.
			continue
		case key == "_has" || strings.HasPrefix(key, "_has:") ||
			key == "_filter" || key == "_query" || key == "_text":
			return nil, fmt.Errorf("%w: %s", ErrUnsupported, key)
		case strings.HasPrefix(key, "_") &&
			key != "_id" && key != "_type" && key != "_lastUpdated" &&
			key != "_tag" && key != "_profile" && key != "_security" &&
			key != "_source":
			return nil, fmt.Errorf("%w: %s", ErrUnsupported, key)
		default:
			conditions, err := parseConditions(key, values)
			if err != nil {
				return nil, err
			}
			query.Conditions = append(query.Conditions, conditions...)
		}
	}
	return query, nil
}

// ParseQuery parses a raw URL query string.
func ParseQuery(resourceType, rawQuery string) (*SearchQuery, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	return ParseSearch(resourceType, values)
}

func singleValue(key string, values []string) (string, error) {
	if len(values) != 1 {
		return "", fmt.Errorf("%w: %s must have exactly one value", ErrInvalidQuery, key)
	}
	return strings.TrimSpace(values[0]), nil
}

func parseNonNegative(key, raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", ErrInvalidQuery, key)
	}
	return n, nil
}

func parseConditions(key string, values []string) ([]Condition, error) {
	condition, err := parseCondition(key)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		condition.Values = []string{""}
		return []Condition{condition}, nil
	}
	conditions := make([]Condition, 0, len(values))
	for _, raw := range values {
		item := condition
		item.Values = splitEscaped(raw, ',')
		conditions = append(conditions, item)
	}
	return conditions, nil
}

func parseCondition(key string) (Condition, error) {
	segments := strings.Split(key, ".")
	if len(segments) == 0 {
		return Condition{}, fmt.Errorf("%w: empty parameter", ErrInvalidQuery)
	}
	condition := Condition{}
	for _, segment := range segments[:len(segments)-1] {
		parts := strings.Split(segment, ":")
		if len(parts) != 2 {
			return Condition{}, fmt.Errorf("%w: chained parameter %q needs target type", ErrInvalidQuery, key)
		}
		param := strings.TrimSpace(parts[0])
		targetType := strings.TrimSpace(parts[1])
		if !validSearchParam(param) {
			return Condition{}, fmt.Errorf("%w: invalid chained parameter %q", ErrInvalidQuery, param)
		}
		if !validResourceType(targetType) {
			return Condition{}, fmt.Errorf("%w: invalid chain target type %q", ErrInvalidQuery, targetType)
		}
		condition.Chain = append(condition.Chain, ChainStep{Param: param, TargetType: targetType})
	}
	if len(condition.Chain) > maxParsedChainHops {
		return Condition{}, fmt.Errorf("%w: chain %q", ErrTooDeep, key)
	}

	terminal := strings.Split(segments[len(segments)-1], ":")
	condition.Param = strings.TrimSpace(terminal[0])
	if !validSearchParam(condition.Param) {
		return Condition{}, fmt.Errorf("%w: invalid parameter %q", ErrInvalidQuery, condition.Param)
	}
	if len(terminal) > 1 {
		modifier := strings.Join(terminal[1:], ":")
		if len(terminal) == 2 && validResourceType(terminal[1]) {
			// subject:Patient without a terminal parameter is not a valid
			// chain and must not be treated as a direct reference filter.
			return Condition{}, fmt.Errorf("%w: chain %q has no terminal parameter", ErrInvalidQuery, key)
		}
		if !validModifier(modifier) {
			return Condition{}, fmt.Errorf("%w: invalid modifier %q", ErrInvalidQuery, modifier)
		}
		condition.Modifier = modifier
	}

	return condition, nil
}

func validModifier(modifier string) bool {
	if modifier == "" {
		return false
	}
	parts := strings.Split(modifier, ":")
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				continue
			}
			return false
		}
	}
	return true
}

func parseIncludeKey(key string, values []string, baseType string) ([]IncludeSpec, error) {
	parts := strings.Split(key, ":")
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: empty include parameter", ErrInvalidQuery)
	}
	reverse := false
	switch parts[0] {
	case "_include":
	case "_revinclude":
		reverse = true
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, key)
	}
	iterate := false
	for _, modifier := range parts[1:] {
		switch strings.ToLower(modifier) {
		case "iterate", "recurse":
			iterate = true
		default:
			return nil, fmt.Errorf("%w: include modifier %q", ErrUnsupported, modifier)
		}
	}

	var specs []IncludeSpec
	for _, raw := range values {
		for _, item := range splitEscaped(raw, ',') {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, fmt.Errorf("%w: empty include value", ErrInvalidQuery)
			}
			spec, err := parseIncludeSpec(item, reverse, iterate, baseType)
			if err != nil {
				return nil, err
			}
			specs = append(specs, spec)
		}
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("%w: empty %s", ErrInvalidQuery, parts[0])
	}
	return specs, nil
}

func parseIncludeSpec(raw string, reverse, iterate bool, baseType string) (IncludeSpec, error) {
	parts := strings.Split(raw, ":")
	spec := IncludeSpec{Iterate: iterate, Reverse: reverse}

	switch len(parts) {
	case 1:
		spec.SourceType = baseType
		spec.Param = strings.TrimSpace(parts[0])
	case 2:
		spec.SourceType = strings.TrimSpace(parts[0])
		spec.Param = strings.TrimSpace(parts[1])
	default:
		spec.SourceType = strings.TrimSpace(parts[0])
		spec.Param = strings.TrimSpace(parts[1])
		for _, target := range parts[2:] {
			typ := strings.TrimSpace(target)
			if typ == "" {
				return IncludeSpec{}, fmt.Errorf("%w: empty include target type", ErrInvalidQuery)
			}
			spec.TargetTypes = append(spec.TargetTypes, typ)
		}
	}

	if spec.SourceType == "*" {
		if reverse {
			return IncludeSpec{}, fmt.Errorf("%w: wildcard _revinclude source type", ErrUnsupported)
		}
	} else if !validResourceType(spec.SourceType) {
		return IncludeSpec{}, fmt.Errorf("%w: invalid include source type %q", ErrInvalidQuery, spec.SourceType)
	}
	if spec.Param != "*" && !validSearchParam(spec.Param) {
		return IncludeSpec{}, fmt.Errorf("%w: invalid include parameter %q", ErrInvalidQuery, spec.Param)
	}
	for _, typ := range spec.TargetTypes {
		if typ != "*" && !validResourceType(typ) {
			return IncludeSpec{}, fmt.Errorf("%w: invalid include target type %q", ErrInvalidQuery, typ)
		}
	}
	return spec, nil
}

// splitEscaped splits on sep while honoring a backslash escape.
func splitEscaped(raw string, sep rune) []string {
	var (
		values  []string
		builder strings.Builder
		escaped bool
	)
	for _, r := range raw {
		if escaped {
			builder.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == sep {
			values = append(values, builder.String())
			builder.Reset()
			continue
		}
		builder.WriteRune(r)
	}
	if escaped {
		builder.WriteRune('\\')
	}
	values = append(values, builder.String())
	return values
}
