package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// PackageURL is a parsed and normalized Package URL (purl).  The exported
// fields contain canonical, decoded values.  Unknown types are preserved and
// marked with Unknown/UnknownType so callers can choose a conservative
// matching policy.
type PackageURL struct {
	Scheme     string
	Type       string
	Namespace  string
	Name       string
	Version    string
	Qualifiers map[string]string
	Subpath    string

	// Raw is the input spelling.  RawType is the type before alias
	// resolution and case normalization.
	Raw     string
	RawType string

	// Unknown and UnknownType are true when no type rules are registered
	// for Type.  In that case component case is preserved.
	Unknown     bool
	UnknownType bool
	Known       bool

	// Aliased reports that a type or package alias was applied.  AliasOf
	// contains the identity of the input coordinate.
	Aliased bool
	AliasOf string

	// IsRange reports that Version is a version range rather than an exact
	// version.  Range is the parsed range.
	IsRange bool
	Range   *VersionRange

	namespaceSegments []string
}

// PURL is an alias for PackageURL.
type PURL = PackageURL

// PackageIdentity is an alias for PackageURL.
type PackageIdentity = PackageURL

type parseOptions struct {
	resolveAliases bool
}

// Parse parses a package URL, applies type-specific normalization and
// resolves known aliases.
func Parse(raw string) (*PackageURL, error) {
	return parsePackageURL(raw, parseOptions{resolveAliases: true})
}

// ParsePURL is an alias for Parse.
func ParsePURL(raw string) (*PackageURL, error) { return Parse(raw) }

// ParsePurl is an alias for Parse.
func ParsePurl(raw string) (*PackageURL, error) { return Parse(raw) }

// ParsePackageURL is an alias for Parse.
func ParsePackageURL(raw string) (*PackageURL, error) { return Parse(raw) }

// FromString is an alias for Parse.
func FromString(raw string) (*PackageURL, error) { return Parse(raw) }

// New parses a purl.  Use NewPackageURL to build from individual components.
func New(raw string) (*PackageURL, error) { return Parse(raw) }

// MustParse parses raw and panics on error.
func MustParse(raw string) *PackageURL {
	p, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return p
}

// ParseWithoutAliases parses a purl but does not apply package aliases.
// Built-in type aliases are still resolved because they are part of purl type
// canonicalization.
func ParseWithoutAliases(raw string) (*PackageURL, error) {
	return parsePackageURL(raw, parseOptions{resolveAliases: false})
}

func parsePackageURL(raw string, opts parseOptions) (*PackageURL, error) {
	originalRaw := raw
	input := strings.TrimSpace(raw)
	if input == "" {
		return nil, fmt.Errorf("empty package URL")
	}
	if len(input) < 5 || !strings.EqualFold(input[:4], "pkg:") {
		return nil, fmt.Errorf("package URL must start with pkg:")
	}
	rest := input[4:]
	if rest == "" {
		return nil, fmt.Errorf("package URL is missing a type")
	}
	if strings.HasPrefix(rest, "//") {
		return nil, fmt.Errorf("package URL must not contain an authority")
	}

	var subpathRaw string
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		subpathRaw = rest[i+1:]
		rest = rest[:i]
		if strings.Contains(subpathRaw, "#") {
			return nil, fmt.Errorf("package URL contains more than one fragment separator")
		}
	}
	var queryRaw string
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		queryRaw = rest[i+1:]
		rest = rest[:i]
	}

	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return nil, fmt.Errorf("package URL is missing a name")
	}
	typeRaw := rest[:slash]
	path := rest[slash+1:]
	typeDecoded, err := decodeComponent(typeRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid type: %w", err)
	}
	if strings.IndexFunc(typeDecoded, whitespaceRune) >= 0 {
		return nil, fmt.Errorf("package type contains whitespace")
	}
	lookupType := asciiLower(typeDecoded)
	canonicalType := lookupType
	aliasUsed := false
	if target, ok := resolveTypeAlias(lookupType); ok {
		canonicalType = target
		aliasUsed = true
	}
	if !validType(canonicalType) {
		return nil, fmt.Errorf("invalid package type %q", typeDecoded)
	}

	var versionRaw string
	hasVersion := false

	// npm dependency aliases are written as name@npm:target@version.  The
	// "@" in the target is normally percent-encoded, but unencoded forms
	// appear in real SBOMs, so recognize them before splitting the version.
	if canonicalType == "npm" {
		if i := strings.Index(path, "@npm:"); i > 0 {
			versionRaw = path[i+1:]
			path = path[:i]
			hasVersion = true
		}
	}
	if !hasVersion {
		if i := strings.LastIndexByte(path, '@'); i > 0 {
			versionRaw = path[i+1:]
			path = path[:i]
			hasVersion = true
		}
	}
	if hasVersion && versionRaw == "" {
		return nil, fmt.Errorf("package URL has an empty version")
	}

	path = strings.Trim(path, "/")
	if path == "" {
		return nil, fmt.Errorf("package URL is missing a name")
	}
	if strings.IndexFunc(path, whitespaceRune) >= 0 {
		return nil, fmt.Errorf("package namespace or name contains unencoded whitespace")
	}
	rawSegments := strings.Split(path, "/")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		if segment == "" {
			return nil, fmt.Errorf("package URL contains an empty namespace segment")
		}
		decoded, err := decodeComponent(segment)
		if err != nil {
			return nil, fmt.Errorf("invalid namespace or name: %w", err)
		}
		if decoded == "" {
			return nil, fmt.Errorf("package URL contains an empty component")
		}
		// An encoded slash is not permitted inside a single purl
		// segment.  Treat it as a segment separator so that
		// "%40angular%2Fcore" and "@angular/core" identify alike.
		for _, part := range strings.Split(decoded, "/") {
			if part == "" {
				return nil, fmt.Errorf("package URL contains an empty component")
			}
			segments = append(segments, part)
		}
	}

	var version string
	if hasVersion {
		version, err = decodeComponent(versionRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid version: %w", err)
		}
		if version == "" {
			return nil, fmt.Errorf("package URL has an empty version")
		}
	}

	qualifiers, err := parseQualifiers(queryRaw)
	if err != nil {
		return nil, err
	}
	subpath, err := parseSubpath(subpathRaw)
	if err != nil {
		return nil, err
	}

	unknown := false
	rule, known := typeRules[canonicalType]
	if !known {
		unknown = true
		if !aliasUsed {
			// Preserve the spelling of unknown types instead of
			// guessing at an ecosystem's case rules.
			canonicalType = typeDecoded
		}
	}

	namespaceSegments := append([]string(nil), segments[:len(segments)-1]...)
	name := segments[len(segments)-1]

	if known {
		switch canonicalType {
		case "pypi", "nuget", "cargo", "oci":
			if len(namespaceSegments) > 0 {
				return nil, fmt.Errorf("package type %s does not allow a namespace", canonicalType)
			}
		case "golang":
			for i := range namespaceSegments {
				namespaceSegments[i] = goModuleUnescape(namespaceSegments[i])
			}
			name = goModuleUnescape(name)
		}
		for i := range namespaceSegments {
			namespaceSegments[i] = applyCase(namespaceSegments[i], rule.namespace)
		}
		name = applyCase(name, rule.name)
	}

	p := &PackageURL{
		Scheme:            "pkg",
		Type:              canonicalType,
		Namespace:         strings.Join(namespaceSegments, "/"),
		Name:              name,
		Version:           version,
		Qualifiers:        qualifiers,
		Subpath:           subpath,
		Raw:               originalRaw,
		RawType:           typeDecoded,
		Unknown:           unknown,
		UnknownType:       unknown,
		Known:             !unknown,
		Aliased:           aliasUsed,
		namespaceSegments: namespaceSegments,
	}
	if p.Aliased {
		p.AliasOf = input
	}

	if vers, ok := qualifiers["vers"]; ok {
		if p.Version != "" {
			return nil, fmt.Errorf("version and vers qualifier are mutually exclusive")
		}
		r, err := ParseVersionRange(vers)
		if err != nil {
			return nil, fmt.Errorf("invalid vers qualifier: %w", err)
		}
		p.Range = r
		p.IsRange = true
	} else if p.Version != "" && known {
		if detectVersionRange(p.Version) {
			r, err := ParseVersionRange(p.Version)
			if err != nil {
				return nil, fmt.Errorf("invalid version range %q: %w", p.Version, err)
			}
			p.Range = r
			p.IsRange = true
			p.Version = r.String()
		} else if v, err := ParseVersion(p.Version); err == nil {
			p.Version = v.String()
		}
	}

	if opts.resolveAliases {
		p = resolvePackageAliases(p, 0)
	}
	return p, nil
}

func parseQualifiers(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			return nil, fmt.Errorf("empty qualifier")
		}
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			return nil, fmt.Errorf("qualifier %q is missing '='", pair)
		}
		key, err := decodeComponent(pair[:eq])
		if err != nil {
			return nil, fmt.Errorf("invalid qualifier key: %w", err)
		}
		value, err := decodeComponent(pair[eq+1:])
		if err != nil {
			return nil, fmt.Errorf("invalid qualifier value: %w", err)
		}
		key = asciiLower(key)
		if strings.IndexFunc(key, whitespaceRune) >= 0 {
			return nil, fmt.Errorf("qualifier key contains whitespace")
		}
		if !validQualifierKey(key) {
			return nil, fmt.Errorf("invalid qualifier key %q", key)
		}
		if key != "vers" && strings.IndexFunc(value, whitespaceRune) >= 0 {
			return nil, fmt.Errorf("qualifier %q contains unencoded whitespace", key)
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("duplicate qualifier key %q", key)
		}
		out[key] = value
	}
	return out, nil
}

func parseSubpath(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", nil
	}
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("empty subpath segment")
		}
		decoded, err := decodeComponent(part)
		if err != nil {
			return "", fmt.Errorf("invalid subpath: %w", err)
		}
		if decoded == "." || decoded == ".." {
			return "", fmt.Errorf("invalid subpath segment %q", decoded)
		}
		if strings.IndexFunc(decoded, whitespaceRune) >= 0 {
			return "", fmt.Errorf("subpath segment contains whitespace")
		}
		if strings.Contains(decoded, "/") {
			return "", fmt.Errorf("subpath segment must not contain a slash")
		}
		out = append(out, decoded)
	}
	return strings.Join(out, "/"), nil
}

// NewPackageURL builds and normalizes a package URL from decoded components.
// The optional qualifiers argument is a single map.
func NewPackageURL(typ, namespace, name, version string, qualifiers ...map[string]string) (*PackageURL, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("empty package name")
	}
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(encodeComponent(strings.TrimSpace(typ)))
	if namespace != "" {
		b.WriteByte('/')
		parts := strings.Split(strings.Trim(namespace, "/"), "/")
		for i, part := range parts {
			if i > 0 {
				b.WriteByte('/')
			}
			b.WriteString(encodeComponent(part))
		}
	}
	b.WriteByte('/')
	b.WriteString(encodeComponent(name))
	if version != "" {
		b.WriteByte('@')
		b.WriteString(encodeComponent(version))
	}
	if len(qualifiers) > 0 && len(qualifiers[0]) > 0 {
		keys := sortedKeys(qualifiers[0])
		b.WriteByte('?')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(encodeComponent(asciiLower(key)))
			b.WriteByte('=')
			b.WriteString(encodeComponent(qualifiers[0][key]))
		}
	}
	return Parse(b.String())
}

// BuildPackageURL is an alias for NewPackageURL.
func BuildPackageURL(typ, namespace, name, version string, qualifiers ...map[string]string) (*PackageURL, error) {
	return NewPackageURL(typ, namespace, name, version, qualifiers...)
}

// Canonicalize parses a purl and returns its canonical spelling.
func Canonicalize(raw string) (string, error) {
	p, err := Parse(raw)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// Normalize is an alias for Canonicalize.
func Normalize(raw string) (string, error) { return Canonicalize(raw) }

// NormalizePURL is an alias for Canonicalize.
func NormalizePURL(raw string) (string, error) { return Canonicalize(raw) }

// Canonical is an alias for Canonicalize.
func Canonical(raw string) (string, error) { return Canonicalize(raw) }

// IdentityOf returns the canonical identity string for raw.
func IdentityOf(raw string) (string, error) {
	p, err := Parse(raw)
	if err != nil {
		return "", err
	}
	return p.Identity(), nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Clone returns a deep copy of the purl.
func (p PackageURL) Clone() PackageURL {
	p.Qualifiers = cloneStringMap(p.Qualifiers)
	p.namespaceSegments = append([]string(nil), p.namespaceSegments...)
	p.Range = p.Range.Clone()
	return p
}

func (p PackageURL) namespaceParts() []string {
	if p.namespaceSegments != nil && strings.Join(p.namespaceSegments, "/") == p.Namespace {
		return p.namespaceSegments
	}
	if p.Namespace == "" {
		return nil
	}
	return strings.Split(p.Namespace, "/")
}

// String returns the canonical purl, including qualifiers and subpath.
func (p PackageURL) String() string {
	return p.canonical(true)
}

// Canonical is an alias for String.
func (p PackageURL) Canonical() string { return p.String() }

// PURL is an alias for String.
func (p PackageURL) PURL() string { return p.String() }

func (p PackageURL) canonical(includeSubpath bool) string {
	typ := p.Type
	if typ == "" {
		typ = p.RawType
	}
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(encodeComponent(typ))
	if ns := p.namespaceParts(); len(ns) > 0 {
		for _, part := range ns {
			b.WriteByte('/')
			b.WriteString(encodeComponent(part))
		}
	}
	b.WriteByte('/')
	b.WriteString(encodeComponent(p.Name))
	if p.Version != "" {
		b.WriteByte('@')
		b.WriteString(encodeComponent(p.Version))
	}
	if len(p.Qualifiers) > 0 {
		keys := sortedKeys(p.Qualifiers)
		first := true
		for _, key := range keys {
			if first {
				b.WriteByte('?')
				first = false
			} else {
				b.WriteByte('&')
			}
			b.WriteString(encodeComponent(asciiLower(key)))
			b.WriteByte('=')
			b.WriteString(encodeComponent(p.Qualifiers[key]))
		}
	}
	if includeSubpath && p.Subpath != "" {
		b.WriteByte('#')
		parts := strings.Split(p.Subpath, "/")
		for i, part := range parts {
			if i > 0 {
				b.WriteByte('/')
			}
			b.WriteString(encodeComponent(part))
		}
	}
	return b.String()
}

// Identity returns the canonical package identity.  The subpath is omitted
// because it identifies a location inside a package rather than a different
// package.  Equivalent versions and version ranges have the same identity.
func (p PackageURL) Identity() string {
	typ := p.Type
	if typ == "" {
		typ = p.RawType
	}
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(encodeComponent(typ))
	if ns := p.namespaceParts(); len(ns) > 0 {
		for _, part := range ns {
			b.WriteByte('/')
			b.WriteString(encodeComponent(part))
		}
	}
	b.WriteByte('/')
	b.WriteString(encodeComponent(p.Name))

	version := p.Version
	if version == "" && p.Range != nil {
		version = p.Range.String()
	}
	if version != "" {
		b.WriteByte('@')
		b.WriteString(encodeComponent(version))
	}

	quals := cloneStringMap(p.Qualifiers)
	delete(quals, "vers")
	if len(quals) > 0 {
		keys := sortedKeys(quals)
		b.WriteByte('?')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(encodeComponent(asciiLower(key)))
			b.WriteByte('=')
			b.WriteString(encodeComponent(quals[key]))
		}
	}
	return b.String()
}

// ID is an alias for Identity.
func (p PackageURL) ID() string { return p.Identity() }

// Key is an alias for Identity.
func (p PackageURL) Key() string { return p.Identity() }

// MatchKey is an alias for Identity.
func (p PackageURL) MatchKey() string { return p.Identity() }

// IdentityWithSubpath returns the canonical purl including the subpath.  Use
// Identity for package-level vulnerability matching.
func (p PackageURL) IdentityWithSubpath() string { return p.String() }

// BaseIdentity returns the type, namespace and name, without version or
// qualifiers.  It is useful for locating a component before evaluating
// vulnerable version ranges.
func (p PackageURL) BaseIdentity() string {
	typ := p.Type
	if typ == "" {
		typ = p.RawType
	}
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(encodeComponent(typ))
	for _, part := range p.namespaceParts() {
		b.WriteByte('/')
		b.WriteString(encodeComponent(part))
	}
	b.WriteByte('/')
	b.WriteString(encodeComponent(p.Name))
	return b.String()
}

// Coordinate is an alias for BaseIdentity.
func (p PackageURL) Coordinate() string { return p.BaseIdentity() }

// Equal reports whether two purls have the same identity.
func (p PackageURL) Equal(other PackageURL) bool {
	return p.Identity() == other.Identity()
}

// EqualPURL reports whether two purl strings have the same identity.
func EqualPURL(a, b string) (bool, error) {
	pa, err := Parse(a)
	if err != nil {
		return false, err
	}
	pb, err := Parse(b)
	if err != nil {
		return false, err
	}
	return pa.Equal(*pb), nil
}

// Identical is an alias for EqualPURL.
func Identical(a, b string) (bool, error) { return EqualPURL(a, b) }

// Equal is an alias for EqualPURL.
func Equal(a, b string) (bool, error) { return EqualPURL(a, b) }

// MatchesPURL reports whether two purls refer to the same package coordinate
// with overlapping versions.
func MatchesPURL(a, b string) (bool, error) {
	pa, err := Parse(a)
	if err != nil {
		return false, err
	}
	pb, err := Parse(b)
	if err != nil {
		return false, err
	}
	return pa.Matches(*pb)
}

// Match is an alias for MatchesPURL.
func Match(a, b string) (bool, error) { return MatchesPURL(a, b) }

// IsKnown reports whether the purl type is known.
func (p PackageURL) IsKnown() bool {
	if p.Unknown || p.UnknownType {
		return false
	}
	if p.Known {
		return true
	}
	_, ok := typeRules[p.Type]
	return ok
}

// IsUnknown reports whether the purl type is unknown.  Unknown purls keep
// their component case and are marked so policies can reject or quarantine
// them.
func (p PackageURL) IsUnknown() bool {
	if p.Unknown || p.UnknownType {
		return true
	}
	if p.Type == "" {
		return false
	}
	_, known := typeRules[asciiLower(p.Type)]
	return !known
}

// Qualifier returns the decoded value for key (case-insensitive).
func (p PackageURL) Qualifier(key string) string {
	if p.Qualifiers == nil {
		return ""
	}
	return p.Qualifiers[asciiLower(key)]
}

// HasQualifier reports whether key is present.
func (p PackageURL) HasQualifier(key string) bool {
	_, ok := p.Qualifiers[asciiLower(key)]
	return ok
}

// ContainsVersion reports whether version is in the purl's version range.  An
// exact-version purl contains only an equal version.
func (p PackageURL) ContainsVersion(version string) (bool, error) {
	if p.Range != nil {
		return p.Range.Contains(version), nil
	}
	if p.Version == "" {
		return false, fmt.Errorf("package URL has no version")
	}
	return CompareVersions(p.Version, version) == 0, nil
}

// MatchesVersion is an alias for ContainsVersion.
func (p PackageURL) MatchesVersion(version string) (bool, error) {
	return p.ContainsVersion(version)
}

// Satisfies reports whether version satisfies the purl's version or version
// range.  Errors are treated as false.
func (p PackageURL) Satisfies(version string) bool {
	ok, err := p.ContainsVersion(version)
	return err == nil && ok
}

// Matches reports whether two package coordinates refer to the same package
// and have overlapping versions.  A purl without a version matches any version
// of the same base package.
func (p PackageURL) Matches(other PackageURL) (bool, error) {
	return p.matches(other, true)
}

// MatchesIgnoringQualifiers is like Matches but treats qualifiers as
// non-identifying metadata.  This is useful when a vulnerability record has a
// less complete coordinate than the SBOM component.
func (p PackageURL) MatchesIgnoringQualifiers(other PackageURL) (bool, error) {
	return p.matches(other, false)
}

func (p PackageURL) matches(other PackageURL, compareQualifiers bool) (bool, error) {
	if p.BaseIdentity() != other.BaseIdentity() {
		return false, nil
	}
	if compareQualifiers && !sameQualifiers(p.Qualifiers, other.Qualifiers) {
		return false, nil
	}
	switch {
	case p.Range != nil && other.Range != nil:
		return p.Range.Intersects(other.Range), nil
	case p.Range != nil && other.Version != "":
		return p.Range.Contains(other.Version), nil
	case other.Range != nil && p.Version != "":
		return other.Range.Contains(p.Version), nil
	case p.Version == "" || other.Version == "":
		return true, nil
	default:
		return CompareVersions(p.Version, other.Version) == 0, nil
	}
}

func sameQualifiers(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// detectVersionRange reports whether a purl version string should be treated
// as a range rather than an exact version.
func detectVersionRange(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if len(s) >= 5 && strings.EqualFold(s[:5], "vers:") {
		return true
	}
	if strings.Contains(s, "||") || strings.Contains(s, "|") || strings.Contains(s, " - ") {
		return true
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") ||
		strings.HasPrefix(s, "[") && strings.HasSuffix(s, ")") ||
		strings.HasPrefix(s, "(") && strings.HasSuffix(s, "]") ||
		strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		return true
	}
	if strings.Contains(s, ",") {
		return true
	}
	if strings.ContainsAny(s[:1], "^~<>=*") {
		return true
	}
	for _, token := range strings.Fields(s) {
		if token == "*" || token == "x" || token == "X" {
			return true
		}
		if len(token) > 0 && strings.ContainsAny(token[:1], "^~<>=!") {
			return true
		}
	}
	return hasWildcardSegment(s)
}

func hasWildcardSegment(s string) bool {
	core := s
	if i := strings.IndexAny(core, "+-"); i >= 0 {
		core = core[:i]
	}
	for _, part := range strings.Split(core, ".") {
		if part == "x" || part == "X" || part == "*" {
			return true
		}
	}
	return false
}
