package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"net/netip"
	"sort"
	"strings"
)

// Generation identifies an immutable version of a bundle, revocation list,
// rotation snapshot, or policy snapshot.
type Generation uint64

func (g Generation) String() string {
	return fmt.Sprintf("%d", uint64(g))
}

// TrustDomain is a canonical, lower-case SPIFFE trust domain.
type TrustDomain string

// ParseTrustDomain validates and canonicalizes a SPIFFE trust domain. Trust
// domains are DNS names: no wildcard, IP literal, port, user-info, path, or
// underscore is accepted.
func ParseTrustDomain(raw string) (TrustDomain, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: empty trust domain", ErrInvalidSPIFFEID)
	}
	if len(raw) > 253 {
		return "", fmt.Errorf("%w: trust domain too long", ErrInvalidSPIFFEID)
	}
	if raw[len(raw)-1] == '.' {
		return "", fmt.Errorf("%w: trust domain must not end with a dot", ErrInvalidSPIFFEID)
	}

	raw = strings.ToLower(raw)
	if _, err := netip.ParseAddr(raw); err == nil {
		return "", fmt.Errorf("%w: trust domain must not be an IP literal", ErrInvalidSPIFFEID)
	}
	if strings.ContainsAny(raw, "*@:/\\?#[]%") {
		return "", fmt.Errorf("%w: invalid character in trust domain", ErrInvalidSPIFFEID)
	}

	for _, label := range strings.Split(raw, ".") {
		if !validDNSLabel(label) {
			return "", fmt.Errorf("%w: invalid label %q", ErrInvalidSPIFFEID, label)
		}
	}
	return TrustDomain(raw), nil
}

func validDNSLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' && i != 0 && i != len(label)-1:
		default:
			return false
		}
	}
	return true
}

// SPIFFEPath is a canonical SPIFFE workload path.
type SPIFFEPath string

const defaultMaxPathDepth = 16

// ParseSPIFFEPath validates a SPIFFE path using the default maximum depth of
// 16 segments. Paths are case-sensitive, must start with '/', must not contain
// empty, "." or ".." segments, wildcards, percent escapes, whitespace, or
// query/fragment characters.
func ParseSPIFFEPath(raw string) (SPIFFEPath, error) {
	return parseSPIFFEPath(raw, defaultMaxPathDepth)
}

func parseSPIFFEPath(raw string, maxDepth int) (SPIFFEPath, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: empty SPIFFE path", ErrInvalidSPIFFEID)
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%w: SPIFFE path must start with '/'", ErrInvalidSPIFFEID)
	}
	if raw == "/" {
		return "", fmt.Errorf("%w: root is not a workload path", ErrInvalidSPIFFEID)
	}
	if strings.HasSuffix(raw, "/") {
		return "", fmt.Errorf("%w: empty trailing path segment", ErrInvalidSPIFFEID)
	}
	segments := strings.Split(raw[1:], "/")
	if len(segments) > maxDepth {
		return "", fmt.Errorf("%w: path depth %d exceeds %d", ErrInvalidSPIFFEID, len(segments), maxDepth)
	}
	for _, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("%w: empty path segment", ErrInvalidSPIFFEID)
		}
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: dot path segment", ErrInvalidSPIFFEID)
		}
		for i := 0; i < len(segment); i++ {
			if !validPathByte(segment[i]) {
				return "", fmt.Errorf("%w: invalid byte in path segment", ErrInvalidSPIFFEID)
			}
		}
	}
	return SPIFFEPath(raw), nil
}

func validPathByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '.', '_', '~', '!', '$', '&', '\'', '(', ')', '+', ',', ';', '=', ':', '@':
		return true
	default:
		return false
	}
}

// SPIFFEID is the only principal form accepted from a workload certificate.
type SPIFFEID struct {
	TrustDomain TrustDomain
	Path        SPIFFEPath
}

// ParseSPIFFEID parses an exact spiffe:// URI. The scheme, trust domain, and
// path are all checked; a URI SAN of another scheme never falls back to a DNS
// or IP identity.
func ParseSPIFFEID(raw string) (SPIFFEID, error) {
	const scheme = "spiffe://"
	if !strings.HasPrefix(raw, scheme) {
		return SPIFFEID{}, fmt.Errorf("%w: scheme must be spiffe", ErrInvalidSPIFFEID)
	}
	rest := raw[len(scheme):]
	if rest == "" {
		return SPIFFEID{}, fmt.Errorf("%w: missing trust domain and path", ErrInvalidSPIFFEID)
	}
	if strings.ContainsAny(rest, "?#") {
		return SPIFFEID{}, fmt.Errorf("%w: query or fragment not allowed", ErrInvalidSPIFFEID)
	}
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return SPIFFEID{}, fmt.Errorf("%w: missing workload path", ErrInvalidSPIFFEID)
	}
	td, err := ParseTrustDomain(rest[:slash])
	if err != nil {
		return SPIFFEID{}, err
	}
	path, err := parseSPIFFEPath(rest[slash:], defaultMaxPathDepth)
	if err != nil {
		return SPIFFEID{}, err
	}
	return SPIFFEID{TrustDomain: td, Path: path}, nil
}

func (id SPIFFEID) String() string {
	if id.TrustDomain == "" || id.Path == "" {
		return ""
	}
	return "spiffe://" + string(id.TrustDomain) + string(id.Path)
}

func (id SPIFFEID) IsZero() bool {
	return id.TrustDomain == "" || id.Path == ""
}

func (id SPIFFEID) Equal(other SPIFFEID) bool {
	return id.TrustDomain == other.TrustDomain && id.Path == other.Path
}

// SANType is a closed set of supported subject alternative name types.
type SANType string

const (
	SANDNS SANType = "dns"
	SANIP  SANType = "ip"
	SANURI SANType = "uri"
)

// SAN is one typed subject alternative name. Type and Value are always
// compared together; a DNS value can never satisfy an IP or URI check.
type SAN struct {
	Type  SANType
	Value string
}

func (s SAN) String() string {
	return string(s.Type) + ":" + s.Value
}

// SANSet is the complete, canonical SAN set extracted from a leaf certificate.
type SANSet []SAN

func normalizeSANSet(in SANSet) SANSet {
	out := append(SANSet(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	if len(out) == 0 {
		return SANSet{}
	}
	dst := 1
	for src := 1; src < len(out); src++ {
		if out[src] == out[dst-1] {
			continue
		}
		out[dst] = out[src]
		dst++
	}
	return out[:dst]
}

func (s SANSet) clone() SANSet {
	return append(SANSet(nil), s...)
}

// DNSNames returns the canonical DNS SAN values.
func (s SANSet) DNSNames() []string {
	var names []string
	for _, san := range s {
		if san.Type == SANDNS {
			names = append(names, san.Value)
		}
	}
	return names
}

// IPAddresses returns the canonical IP SAN values.
func (s SANSet) IPAddresses() []netip.Addr {
	var addrs []netip.Addr
	for _, san := range s {
		if san.Type == SANIP {
			if addr, err := netip.ParseAddr(san.Value); err == nil {
				addrs = append(addrs, addr)
			}
		}
	}
	return addrs
}

// URIs returns the SPIFFE URI SAN values.
func (s SANSet) URIs() []SPIFFEID {
	var ids []SPIFFEID
	for _, san := range s {
		if san.Type == SANURI {
			if id, err := ParseSPIFFEID(san.Value); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (s SANSet) fingerprint() [32]byte {
	h := sha256.New()
	writeHashString(h, "san-set-v1")
	writeHashString(h, fmt.Sprintf("%d", len(s)))
	for _, san := range s {
		writeHashString(h, string(san.Type))
		writeHashString(h, san.Value)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// WorkloadIdentity is the verified identity of one workload. Its fingerprint
// commits to the trust domain, SPIFFE path, complete typed SAN set, certificate
// chain, and attested node.
type WorkloadIdentity struct {
	TrustDomain          TrustDomain
	SPIFFEID             SPIFFEID
	SANs                 SANSet
	ChainFingerprint     [32]byte
	Node                 NodeIdentity
	BundleGeneration     Generation
	RevocationGeneration Generation
	RotationGeneration   Generation
}

func (id WorkloadIdentity) String() string {
	return id.SPIFFEID.String()
}

// Fingerprint returns a stable digest over all identity-defining inputs. It
// deliberately excludes generation counters: a bundle refresh does not change
// the workload principal, but it may change whether that principal is trusted.
func (id WorkloadIdentity) Fingerprint() [32]byte {
	h := sha256.New()
	writeHashString(h, "workload-identity-v1")
	writeHashString(h, string(id.TrustDomain))
	writeHashString(h, string(id.SPIFFEID.Path))
	sanFingerprint := normalizeSANSet(id.SANs).fingerprint()
	writeHashString(h, string(sanFingerprint[:]))
	h.Write(id.ChainFingerprint[:])
	writeHashString(h, id.Node.NodeID)
	writeHashString(h, string(id.Node.TrustDomain))
	keys := make([]string, 0, len(id.Node.Selectors))
	for key := range id.Node.Selectors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeHashString(h, key)
		writeHashString(h, id.Node.Selectors[key])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeHashString(h hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}
