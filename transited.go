package krb5policy

import (
	"fmt"
	"strings"
	"unicode"
)

// TransitedDomainX500Compress is the only registered RFC 4120 transited
// encoding type.
const TransitedDomainX500Compress int32 = 1

// TransitedEncoding is the RFC 4120 TransitedEncoding structure.
type TransitedEncoding struct {
	Type     int32
	Contents []byte
}

// NewTransitedEncoding builds a DOMAIN-X500-COMPRESS transited encoding.
func NewTransitedEncoding(contents []byte) TransitedEncoding {
	return TransitedEncoding{
		Type:     TransitedDomainX500Compress,
		Contents: append([]byte(nil), contents...),
	}
}

// Limits hard-bound every allocation and traversal derived from an attacker
// controlled transited field.  Zero values select conservative defaults.
type Limits struct {
	MaxEncodedBytes   int
	MaxItems          int
	MaxRealmBytes     int
	MaxComponents     int
	MaxExpandedBytes  int
	MaxExpandedRealms int
}

// DefaultLimits returns the package defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxEncodedBytes:   64 * 1024,
		MaxItems:          128,
		MaxRealmBytes:     1024,
		MaxComponents:     64,
		MaxExpandedBytes:  128 * 1024,
		MaxExpandedRealms: 128,
	}
}

func (l Limits) withDefaults() Limits {
	def := DefaultLimits()
	if l.MaxEncodedBytes <= 0 {
		l.MaxEncodedBytes = def.MaxEncodedBytes
	}
	if l.MaxItems <= 0 {
		l.MaxItems = def.MaxItems
	}
	if l.MaxRealmBytes <= 0 {
		l.MaxRealmBytes = def.MaxRealmBytes
	}
	if l.MaxComponents <= 0 {
		l.MaxComponents = def.MaxComponents
	}
	if l.MaxExpandedBytes <= 0 {
		l.MaxExpandedBytes = def.MaxExpandedBytes
	}
	if l.MaxExpandedRealms <= 0 {
		l.MaxExpandedRealms = def.MaxExpandedRealms
	}
	return l
}

// TransitedOptions controls expansion.  A trailing comma in the encoded field
// always disables implicit final-realm completion, regardless of
// DisableImplicitFinal.
type TransitedOptions struct {
	Limits Limits

	// DisableImplicitFinal prevents the decoder from considering the server
	// realm as the final closed hop when the encoded field itself does not end
	// in a trailing comma.  The default is false.
	DisableImplicitFinal bool

	// AllowInteriorEmpty treats an empty interior item as "same realm as the
	// previous item".  It is enabled by default for peer compatibility.  Path
	// validation still rejects cycles and duplicate endpoints.
	DisallowInteriorEmpty bool
}

// ExpandedTransited is the result of decoding one transited field.
//
// Realms contains only the explicitly encoded intermediate realms.  FullPath
// starts with the client realm, includes Realms, and includes the server realm
// when implicit completion is enabled and the field was not terminated by a
// trailing comma.
type ExpandedTransited struct {
	Realms             []string
	FullPath           []string
	TrailingComma      bool
	ImplicitFinalAdded bool
	Complete           bool
	Encoded            string
}

// EncodeOptions controls canonical DOMAIN-X500-COMPRESS encoding.
type EncodeOptions struct {
	Limits Limits

	// TrailingComma appends the RFC empty final item.  It tells a decoder not
	// to implicitly complete the path using the service realm.
	TrailingComma bool
}

type realmKind int

const (
	realmKindSingle realmKind = iota
	realmKindDNS
	realmKindX500
)

func classifyRealm(realm string) realmKind {
	if strings.Contains(realm, "/") {
		return realmKindX500
	}
	if strings.Contains(realm, ".") {
		return realmKindDNS
	}
	return realmKindSingle
}

func x500Components(realm string) ([]string, error) {
	realm = strings.TrimSpace(realm)
	for strings.HasPrefix(realm, "/") {
		realm = realm[1:]
	}
	if realm == "" {
		return nil, fmt.Errorf("%w: empty X.500 realm", ErrTransitedMalformed)
	}
	if strings.HasPrefix(realm, "/") || strings.HasSuffix(realm, "/") {
		return nil, fmt.Errorf("%w: empty X.500 component", ErrTransitedMalformed)
	}
	parts := strings.Split(realm, "/")
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("%w: empty X.500 component", ErrTransitedMalformed)
		}
	}
	return parts, nil
}

func dnsComponents(realm string) ([]string, error) {
	if realm == "" {
		return nil, fmt.Errorf("%w: empty DNS realm", ErrTransitedMalformed)
	}
	parts := strings.Split(realm, ".")
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("%w: empty DNS component", ErrTransitedMalformed)
		}
	}
	return parts, nil
}

func checkRealmBound(realm string, lim Limits) error {
	if realm == "" {
		return fmt.Errorf("%w: empty realm", ErrTransitedMalformed)
	}
	if len(realm) > lim.MaxRealmBytes {
		return fmt.Errorf("%w: realm length %d > %d", ErrTransitedLimit, len(realm), lim.MaxRealmBytes)
	}
	for _, r := range realm {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("%w: control character in realm", ErrTransitedMalformed)
		}
	}
	var n int
	switch classifyRealm(realm) {
	case realmKindX500:
		c, err := x500Components(realm)
		if err != nil {
			return err
		}
		n = len(c)
	case realmKindDNS:
		c, err := dnsComponents(realm)
		if err != nil {
			return err
		}
		n = len(c)
	default:
		n = 1
	}
	if n > lim.MaxComponents {
		return fmt.Errorf("%w: realm has %d components > %d", ErrTransitedLimit, n, lim.MaxComponents)
	}
	return nil
}

// EncodeTransited produces a bounded DOMAIN-X500-COMPRESS field.
//
// The canonical form uses two relative abbreviations:
//   - A leading '/' followed by one or more slash components replaces the
//     corresponding number of trailing components of the previous X.500 path.
//     This is the natural sibling abbreviation for hierarchical X.500 names.
//   - A leading '.' followed by one or more dot components replaces the
//     corresponding number of leading components of the previous DNS name,
//     retaining its shared suffix.  This is the DNS suffix abbreviation.
//
// A realm that cannot be abbreviated safely is emitted in full.  A leading
// comma is never produced; a literal trailing comma is opt-in.
func EncodeTransited(realms []string, opts EncodeOptions) ([]byte, error) {
	lim := opts.Limits.withDefaults()
	if len(realms) > lim.MaxItems {
		return nil, fmt.Errorf("%w: %d realms > %d", ErrTransitedLimit, len(realms), lim.MaxItems)
	}
	if len(realms) == 0 {
		if opts.TrailingComma {
			return []byte(","), nil
		}
		return nil, nil
	}

	var encoded strings.Builder
	var prev string
	for i, realm := range realms {
		realm = strings.TrimSpace(realm)
		if strings.ContainsAny(realm, ",\x00\r\n") {
			return nil, fmt.Errorf("%w: realm %q contains a forbidden delimiter", ErrTransitedMalformed, realm)
		}
		if err := checkRealmBound(realm, lim); err != nil {
			return nil, err
		}
		item := realm
		if i > 0 {
			var err error
			item, err = abbreviateRealm(prev, realm, lim)
			if err != nil {
				return nil, err
			}
		}
		if i > 0 {
			encoded.WriteByte(',')
		}
		encoded.WriteString(item)
		if encoded.Len() > lim.MaxEncodedBytes {
			return nil, fmt.Errorf("%w: encoded length exceeds %d", ErrTransitedLimit, lim.MaxEncodedBytes)
		}
		prev = realm
	}
	if opts.TrailingComma {
		encoded.WriteByte(',')
	}
	if encoded.Len() > lim.MaxEncodedBytes {
		return nil, fmt.Errorf("%w: encoded length exceeds %d", ErrTransitedLimit, lim.MaxEncodedBytes)
	}
	return []byte(encoded.String()), nil
}

func abbreviateRealm(prev, curr string, lim Limits) (string, error) {
	a, b := classifyRealm(prev), classifyRealm(curr)
	if a != b {
		return curr, nil
	}
	switch a {
	case realmKindX500:
		pc, err := x500Components(prev)
		if err != nil {
			return "", err
		}
		cc, err := x500Components(curr)
		if err != nil {
			return "", err
		}
		if len(pc) != len(cc) {
			return curr, nil
		}
		common := 0
		for common < len(pc) && componentEqual(pc[common], cc[common]) {
			common++
		}
		if common == 0 || common == len(pc) {
			return curr, nil
		}
		suffix := cc[common:]
		if len(suffix) > lim.MaxComponents {
			return "", fmt.Errorf("%w: too many relative components", ErrTransitedLimit)
		}
		return "/" + strings.Join(suffix, "/"), nil
	case realmKindDNS:
		pc, err := dnsComponents(prev)
		if err != nil {
			return "", err
		}
		cc, err := dnsComponents(curr)
		if err != nil {
			return "", err
		}
		if len(pc) != len(cc) {
			return curr, nil
		}
		common := 0
		for common < len(pc) && componentEqual(pc[len(pc)-1-common], cc[len(cc)-1-common]) {
			common++
		}
		if common == 0 || common == len(pc) {
			return curr, nil
		}
		prefix := cc[:len(cc)-common]
		if len(prefix) > lim.MaxComponents {
			return "", fmt.Errorf("%w: too many relative components", ErrTransitedLimit)
		}
		return "." + strings.Join(prefix, "."), nil
	default:
		return curr, nil
	}
}

func componentEqual(a, b string) bool {
	return strings.EqualFold(a, b)
}

// ExpandTransited decodes and expands a bounded DOMAIN-X500-COMPRESS field.
//
// The first relative item is expanded relative to clientRealm.  Subsequent
// relative items are expanded relative to the immediately preceding expanded
// realm.  Relative expansion is strict: a relative DNS name must fit inside
// the previous name's component depth, and a relative X.500 suffix may not
// replace more components than its parent contains.
func ExpandTransited(encoded []byte, clientRealm, serverRealm string, opts TransitedOptions) (*ExpandedTransited, error) {
	lim := opts.Limits.withDefaults()
	if len(encoded) > lim.MaxEncodedBytes {
		return nil, fmt.Errorf("%w: encoded length %d > %d", ErrTransitedLimit, len(encoded), lim.MaxEncodedBytes)
	}
	s := string(encoded)
	if strings.ContainsAny(s, "\x00\r\n") {
		return nil, fmt.Errorf("%w: control character in transited field", ErrTransitedMalformed)
	}
	if clientRealm != "" {
		if err := checkRealmBound(clientRealm, lim); err != nil {
			return nil, fmt.Errorf("%w: client realm: %v", ErrTransitedMalformed, err)
		}
	}
	if serverRealm != "" {
		if err := checkRealmBound(serverRealm, lim); err != nil {
			return nil, fmt.Errorf("%w: server realm: %v", ErrTransitedMalformed, err)
		}
	}

	exp := &ExpandedTransited{Encoded: s}
	if s == "" {
		implicit := !opts.DisableImplicitFinal
		exp.Realms = []string{}
		exp.FullPath = buildFullPath(clientRealm, nil, serverRealm, implicit)
		exp.ImplicitFinalAdded = implicit && serverRealm != "" && !sameRealm(clientRealm, serverRealm)
		exp.Complete = isPathClosed(clientRealm, exp.FullPath, serverRealm)
		return exp, nil
	}

	items := strings.Split(s, ",")
	if len(items) > lim.MaxItems+1 {
		return nil, fmt.Errorf("%w: %d items > %d", ErrTransitedLimit, len(items), lim.MaxItems)
	}
	if strings.HasSuffix(s, ",") {
		exp.TrailingComma = true
		items = items[:len(items)-1]
	}
	if len(items) > lim.MaxItems {
		return nil, fmt.Errorf("%w: %d items > %d", ErrTransitedLimit, len(items), lim.MaxItems)
	}
	if len(items) == 1 && items[0] == "" && exp.TrailingComma {
		exp.Realms = []string{}
		exp.FullPath = buildFullPath(clientRealm, nil, serverRealm, false)
		exp.Complete = isPathClosed(clientRealm, exp.FullPath, serverRealm)
		return exp, nil
	}

	exp.Realms = make([]string, 0, len(items))
	base := clientRealm
	expandedBytes := 0
	for i, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" {
			if i == len(items)-1 && exp.TrailingComma {
				break
			}
			if opts.DisallowInteriorEmpty {
				return nil, fmt.Errorf("%w: empty interior transited item", ErrTransitedMalformed)
			}
			if base == "" {
				return nil, fmt.Errorf("%w: empty item has no base realm", ErrTransitedMalformed)
			}
			item = base
		}
		realm, err := expandItem(item, base, lim)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		if err := checkRealmBound(realm, lim); err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		expandedBytes += len(realm)
		if expandedBytes > lim.MaxExpandedBytes {
			return nil, fmt.Errorf("%w: expanded bytes exceed %d", ErrTransitedLimit, lim.MaxExpandedBytes)
		}
		exp.Realms = append(exp.Realms, realm)
		if len(exp.Realms) > lim.MaxExpandedRealms {
			return nil, fmt.Errorf("%w: expanded realms exceed %d", ErrTransitedLimit, lim.MaxExpandedRealms)
		}
		base = realm
	}

	implicit := !exp.TrailingComma && !opts.DisableImplicitFinal
	exp.FullPath = buildFullPath(clientRealm, exp.Realms, serverRealm, implicit)
	exp.ImplicitFinalAdded = implicit && serverRealm != "" && (len(exp.Realms) == 0 || !sameRealm(exp.Realms[len(exp.Realms)-1], serverRealm))
	exp.Complete = isPathClosed(clientRealm, exp.FullPath, serverRealm)
	return exp, nil
}

func buildFullPath(client string, realms []string, server string, implicit bool) []string {
	full := make([]string, 0, len(realms)+2)
	if client != "" {
		full = append(full, client)
	}
	full = append(full, realms...)
	if implicit && server != "" && (len(realms) == 0 || !sameRealm(realms[len(realms)-1], server)) {
		full = append(full, server)
	}
	return full
}

func isPathClosed(client string, full []string, server string) bool {
	if server == "" {
		return true
	}
	if len(full) == 0 {
		return false
	}
	return sameRealm(full[len(full)-1], server)
}

func expandItem(item, base string, lim Limits) (string, error) {
	if strings.HasPrefix(item, "//") {
		absolute := "/" + strings.TrimPrefix(item, "//")
		if err := checkRealmBound(absolute, lim); err != nil {
			return "", err
		}
		return absolute, nil
	}
	switch {
	case strings.HasPrefix(item, "."):
		if base == "" {
			return "", fmt.Errorf("%w: relative DNS realm without a base", ErrTransitedMalformed)
		}
		pc, err := dnsComponents(base)
		if err != nil {
			return "", fmt.Errorf("%w: DNS relative expansion base %q is not DNS", ErrTransitedMalformed, base)
		}
		rel := strings.TrimPrefix(item, ".")
		if rel == "" {
			return "", fmt.Errorf("%w: empty relative DNS realm", ErrTransitedMalformed)
		}
		rc, err := dnsComponents(rel)
		if err != nil {
			return "", err
		}
		if len(rc) > len(pc) {
			return "", fmt.Errorf("%w: relative DNS realm %q has more components than base %q", ErrTransitedMalformed, item, base)
		}
		out := make([]string, 0, len(pc))
		out = append(out, rc...)
		out = append(out, pc[len(rc):]...)
		return strings.Join(out, "."), nil
	case strings.HasPrefix(item, "/"):
		if base == "" || classifyRealm(base) != realmKindX500 {
			if err := checkRealmBound(item, lim); err != nil {
				return "", err
			}
			return item, nil
		}
		rc, err := x500Components(item)
		if err != nil {
			return "", err
		}
		pc, err := x500Components(base)
		if err != nil {
			return "", err
		}
		if len(rc) >= len(pc) || componentEqual(rc[0], pc[0]) {
			if err := checkRealmBound(item, lim); err != nil {
				return "", err
			}
			return item, nil
		}
		out := make([]string, 0, len(pc))
		out = append(out, pc[:len(pc)-len(rc)]...)
		out = append(out, rc...)
		return "/" + strings.Join(out, "/"), nil
	default:
		if err := checkRealmBound(item, lim); err != nil {
			return "", err
		}
		return item, nil
	}
}

// ValidateTransitedEncoding checks the registered encoding type and applies
// the encoded-byte bound.
func ValidateTransitedEncoding(te TransitedEncoding, limits Limits) error {
	lim := limits.withDefaults()
	if te.Type != TransitedDomainX500Compress {
		return fmt.Errorf("%w: type %d", ErrTransitedEncoding, te.Type)
	}
	if len(te.Contents) > lim.MaxEncodedBytes {
		return fmt.Errorf("%w: %d bytes > %d", ErrTransitedLimit, len(te.Contents), lim.MaxEncodedBytes)
	}
	for _, b := range te.Contents {
		if b == 0 || b == '\r' || b == '\n' {
			return fmt.Errorf("%w: forbidden byte in transited field", ErrTransitedMalformed)
		}
	}
	return nil
}

// Expand expands an RFC 4120 TransitedEncoding after checking its type.
func (te TransitedEncoding) Expand(clientRealm, serverRealm string, opts TransitedOptions) (*ExpandedTransited, error) {
	if err := ValidateTransitedEncoding(te, opts.Limits); err != nil {
		return nil, err
	}
	return ExpandTransited(te.Contents, clientRealm, serverRealm, opts)
}
