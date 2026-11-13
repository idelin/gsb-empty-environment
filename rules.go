package netsec

import (
	"path"
	"regexp"
	"strings"
)

// FieldRule is a field-level allow rule. Every constrained value must match at
// least one Exact, Prefix or Regex entry when any such entry is present. If a
// value matches a ForbiddenKeys entry (or an object contains such a key), the
// request is denied. An empty FieldRule only applies the unconditional
// security checks.
type FieldRule struct {
	Exact         []string `json:"exact,omitempty"`
	Prefix        []string `json:"prefix,omitempty"`
	Regex         []string `json:"regex,omitempty"`
	ForbiddenKeys []string `json:"forbidden_keys,omitempty"`

	// Alternate spellings accepted for callers with an existing rule schema.
	ExactValues []string `json:"exact_values,omitempty"`
	Prefixes    []string `json:"prefixes,omitempty"`
	Patterns    []string `json:"patterns,omitempty"`
	DenyKeys    []string `json:"deny_keys,omitempty"`

	// Forbidden is an alias for ForbiddenKeys accepted when rules are built
	// by callers that use a shorter name.
	Forbidden []string `json:"forbidden,omitempty"`
}

type FieldRules = FieldRule

// MethodRules contains the allowed fields and their patterns for one method.
// Fields is the normal form; the direct maps are merged into it and make
// small policy literals less verbose.
type MethodRules struct {
	Fields        map[string]FieldRule `json:"fields,omitempty"`
	FieldRules    map[string]FieldRule `json:"field_rules,omitempty"`
	Exact         map[string][]string  `json:"exact,omitempty"`
	Prefix        map[string][]string  `json:"prefix,omitempty"`
	Regex         map[string][]string  `json:"regex,omitempty"`
	ForbiddenKeys map[string][]string  `json:"forbidden_keys,omitempty"`
}

type MethodRule = MethodRules

// Rules is the host-approved request policy. A method is allowed only when it
// is present in Methods, or listed in AllowedMethods/Allowed. If neither form
// is populated, every method is denied.
type Rules struct {
	Methods        map[string]MethodRules `json:"methods,omitempty"`
	MethodRules    map[string]MethodRules `json:"method_rules,omitempty"`
	Allowed        []string               `json:"allowed,omitempty"`
	AllowedMethods []string               `json:"allowed_methods,omitempty"`

	// Global field rules are applied to every approved method. Method-specific
	// rules for the same field are combined with them.
	Fields        map[string]FieldRule `json:"fields,omitempty"`
	FieldRules    map[string]FieldRule `json:"field_rules,omitempty"`
	Exact         map[string][]string  `json:"exact,omitempty"`
	Prefix        map[string][]string  `json:"prefix,omitempty"`
	Regex         map[string][]string  `json:"regex,omitempty"`
	ForbiddenKeys map[string][]string  `json:"forbidden_keys,omitempty"`

	// ForbiddenKeysAny is a request-wide key blacklist. These names are
	// matched against object keys at every level of the payload.
	ForbiddenKeysAny []string `json:"forbidden_keys_any,omitempty"`

	// Forbidden is an alias for ForbiddenKeysAny.
	Forbidden []string `json:"forbidden,omitempty"`
}

type RuleSet = Rules
type PolicyRules = Rules

func (r FieldRule) forbiddenKeys() []string {
	if len(r.ForbiddenKeys) == 0 && len(r.Forbidden) == 0 && len(r.DenyKeys) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.ForbiddenKeys)+len(r.Forbidden)+len(r.DenyKeys))
	out = append(out, r.ForbiddenKeys...)
	out = append(out, r.Forbidden...)
	out = append(out, r.DenyKeys...)
	return out
}

func (r FieldRule) constrained() bool {
	return len(r.Exact) > 0 || len(r.Prefix) > 0 || len(r.Regex) > 0 ||
		len(r.ExactValues) > 0 || len(r.Prefixes) > 0 || len(r.Patterns) > 0
}

// Matches reports whether value is allowed by the Exact/Prefix/Regex entries.
// Forbidden keys are object-key constraints and are checked separately.
func (r FieldRule) Matches(value string) bool {
	if !r.constrained() {
		return true
	}
	for _, exact := range append(append([]string{}, r.Exact...), r.ExactValues...) {
		if value == exact {
			return true
		}
	}
	for _, prefix := range append(append([]string{}, r.Prefix...), r.Prefixes...) {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	for _, pattern := range append(append([]string{}, r.Regex...), r.Patterns...) {
		re, err := regexp.Compile(pattern)
		if err == nil && re.MatchString(value) {
			return true
		}
	}
	return false
}

// MatchField is the package-level form of FieldRule.Matches.
func MatchField(rule FieldRule, value string) bool {
	return rule.Matches(value)
}

func (r FieldRule) keywordForbidden(key string) bool {
	for _, forbidden := range r.forbiddenKeys() {
		if keyMatches(forbidden, key) {
			return true
		}
	}
	return false
}

func keyMatches(pattern, key string) bool {
	if pattern == key {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(key, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func methodKey(method string) string {
	method = strings.TrimSpace(method)
	if i := strings.LastIndex(method, "/"); i >= 0 {
		method = method[i+1:]
	}
	return normalizeName(method)
}

func normalizeName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '_', '-', '.', '/', ' ':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}

func canonicalMethod(name string) string {
	switch methodKey(name) {
	case "createsandbox":
		return MethodCreateSandbox
	case "createcontainer":
		return MethodCreateContainer
	case "startcontainer":
		return MethodStartContainer
	case "execprocess":
		return MethodExecProcess
	case "copyfile":
		return MethodCopyFile
	default:
		return strings.TrimSpace(name)
	}
}

func fieldPathName(name string) string {
	clean := strings.TrimSpace(strings.ToLower(name))
	clean = strings.ReplaceAll(clean, "\\", "/")
	clean = strings.ReplaceAll(clean, ".", "/")
	clean = path.Clean(clean)
	if clean == "." {
		return ""
	}
	clean = strings.TrimPrefix(clean, "/")
	return normalizeName(clean)
}

func mergeFieldRule(a, b FieldRule) FieldRule {
	var out FieldRule
	out.Exact = append(append([]string{}, a.Exact...), b.Exact...)
	out.Prefix = append(append([]string{}, a.Prefix...), b.Prefix...)
	out.Regex = append(append([]string{}, a.Regex...), b.Regex...)
	out.ExactValues = append(append([]string{}, a.ExactValues...), b.ExactValues...)
	out.Prefixes = append(append([]string{}, a.Prefixes...), b.Prefixes...)
	out.Patterns = append(append([]string{}, a.Patterns...), b.Patterns...)
	out.ForbiddenKeys = append(append([]string{}, a.ForbiddenKeys...), b.ForbiddenKeys...)
	out.Forbidden = append(append([]string{}, a.Forbidden...), b.Forbidden...)
	out.DenyKeys = append(append([]string{}, a.DenyKeys...), b.DenyKeys...)
	return out
}

func cloneFieldRule(rule FieldRule) FieldRule {
	return mergeFieldRule(FieldRule{}, rule)
}

func cloneRules(in Rules) Rules {
	out := Rules{
		Allowed:          append([]string{}, in.Allowed...),
		AllowedMethods:   append([]string{}, in.AllowedMethods...),
		ForbiddenKeysAny: append([]string{}, in.ForbiddenKeysAny...),
		Forbidden:        append([]string{}, in.Forbidden...),
	}
	if in.Methods != nil {
		out.Methods = make(map[string]MethodRules, len(in.Methods))
		for name, rule := range in.Methods {
			out.Methods[name] = mergeMethodRules(MethodRules{}, rule)
		}
	}
	if in.MethodRules != nil {
		out.MethodRules = make(map[string]MethodRules, len(in.MethodRules))
		for name, rule := range in.MethodRules {
			out.MethodRules[name] = mergeMethodRules(MethodRules{}, rule)
		}
	}
	out.Fields = cloneFieldMap(in.Fields)
	out.FieldRules = cloneFieldMap(in.FieldRules)
	out.Exact = cloneStringListMap(in.Exact)
	out.Prefix = cloneStringListMap(in.Prefix)
	out.Regex = cloneStringListMap(in.Regex)
	out.ForbiddenKeys = cloneStringListMap(in.ForbiddenKeys)
	return out
}

func cloneFieldMap(in map[string]FieldRule) map[string]FieldRule {
	if in == nil {
		return nil
	}
	out := make(map[string]FieldRule, len(in))
	for name, rule := range in {
		out[name] = cloneFieldRule(rule)
	}
	return out
}

func cloneStringListMap(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, values := range in {
		out[name] = append([]string{}, values...)
	}
	return out
}

func (r Rules) methodRule(method string) (MethodRules, bool) {
	key := methodKey(method)
	var found MethodRules
	var ok bool
	merge := func(m map[string]MethodRules) {
		for name, rule := range m {
			if methodKey(name) != key {
				continue
			}
			found = mergeMethodRules(found, rule)
			ok = true
		}
	}
	merge(r.Methods)
	merge(r.MethodRules)

	listed := false
	for _, name := range append(append([]string{}, r.Allowed...), r.AllowedMethods...) {
		if methodKey(name) == key || methodKey(name) == methodKey(canonicalMethod(method)) {
			listed = true
			break
		}
	}
	if ok || listed {
		return found, true
	}
	return MethodRules{}, false
}

func mergeMethodRules(a, b MethodRules) MethodRules {
	out := MethodRules{
		Fields:        map[string]FieldRule{},
		FieldRules:    map[string]FieldRule{},
		Exact:         map[string][]string{},
		Prefix:        map[string][]string{},
		Regex:         map[string][]string{},
		ForbiddenKeys: map[string][]string{},
	}
	for k, v := range a.Fields {
		out.Fields[k] = v
	}
	for k, v := range b.Fields {
		out.Fields[k] = mergeFieldRule(out.Fields[k], v)
	}
	for k, v := range a.FieldRules {
		out.FieldRules[k] = v
	}
	for k, v := range b.FieldRules {
		out.FieldRules[k] = mergeFieldRule(out.FieldRules[k], v)
	}
	for k, v := range a.Exact {
		out.Exact[k] = append(out.Exact[k], v...)
	}
	for k, v := range b.Exact {
		out.Exact[k] = append(out.Exact[k], v...)
	}
	for k, v := range a.Prefix {
		out.Prefix[k] = append(out.Prefix[k], v...)
	}
	for k, v := range b.Prefix {
		out.Prefix[k] = append(out.Prefix[k], v...)
	}
	for k, v := range a.Regex {
		out.Regex[k] = append(out.Regex[k], v...)
	}
	for k, v := range b.Regex {
		out.Regex[k] = append(out.Regex[k], v...)
	}
	for k, v := range a.ForbiddenKeys {
		out.ForbiddenKeys[k] = append(out.ForbiddenKeys[k], v...)
	}
	for k, v := range b.ForbiddenKeys {
		out.ForbiddenKeys[k] = append(out.ForbiddenKeys[k], v...)
	}
	return out
}

func (r Rules) fieldRule(method string, names ...string) FieldRule {
	methodRules, _ := r.methodRule(method)
	local := selectFieldRule(methodRules, names)
	global := selectGlobalFieldRule(r, names)
	return mergeFieldRule(global, local)
}

func mergeNameMaps(a, b map[string]FieldRule) map[string]FieldRule {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]FieldRule, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = mergeFieldRule(out[k], v)
	}
	return out
}

func selectFieldRule(methodRules MethodRules, names []string) FieldRule {
	var out FieldRule
	for name, rule := range mergeNameMaps(methodRules.Fields, methodRules.FieldRules) {
		if fieldMatchesAny(name, names) {
			out = mergeFieldRule(out, rule)
		}
	}
	for name, values := range methodRules.Exact {
		if fieldMatchesAny(name, names) {
			out.Exact = append(out.Exact, values...)
		}
	}
	for name, values := range methodRules.Prefix {
		if fieldMatchesAny(name, names) {
			out.Prefix = append(out.Prefix, values...)
		}
	}
	for name, values := range methodRules.Regex {
		if fieldMatchesAny(name, names) {
			out.Regex = append(out.Regex, values...)
		}
	}
	for name, values := range methodRules.ForbiddenKeys {
		if fieldMatchesAny(name, names) {
			out.ForbiddenKeys = append(out.ForbiddenKeys, values...)
		}
	}
	return out
}

func selectGlobalFieldRule(r Rules, names []string) FieldRule {
	var out FieldRule
	for name, rule := range mergeNameMaps(r.Fields, r.FieldRules) {
		if fieldMatchesAny(name, names) {
			out = mergeFieldRule(out, rule)
		}
	}
	for name, values := range r.Exact {
		if fieldMatchesAny(name, names) {
			out.Exact = append(out.Exact, values...)
		}
	}
	for name, values := range r.Prefix {
		if fieldMatchesAny(name, names) {
			out.Prefix = append(out.Prefix, values...)
		}
	}
	for name, values := range r.Regex {
		if fieldMatchesAny(name, names) {
			out.Regex = append(out.Regex, values...)
		}
	}
	for name, values := range r.ForbiddenKeys {
		if fieldMatchesAny(name, names) {
			out.ForbiddenKeys = append(out.ForbiddenKeys, values...)
		}
	}
	return out
}

func fieldMatchesAny(name string, candidates []string) bool {
	normalized := fieldPathName(name)
	for _, candidate := range candidates {
		if candidate == "*" {
			return true
		}
		if normalized == fieldPathName(candidate) {
			return true
		}
	}
	return false
}
