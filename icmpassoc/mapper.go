package icmpassoc

// AddrMapper translates addresses seen inside the quoted original datagram
// into addresses in the host's connection namespace.
//
// Implementations are version-bound: Version must report the single address
// family they accept and Translate must reject every other family with
// ErrVersionMismatch. This prevents an IPv4 rule from ever touching an IPv6
// packet (or vice versa), including IPv4-mapped IPv6 addresses.
type AddrMapper interface {
	Version() IPVersion
	Translate(Endpoint) (Endpoint, error)
}

// StaticMapper is an immutable, version-locked address/port translation
// table supplied by the caller. Lookups that miss return the input endpoint
// unchanged so flows that did not undergo translation still match.
type StaticMapper struct {
	version IPVersion
	rules   map[Endpoint]Endpoint
}

// NewStaticMapper builds a mapper for one fixed address family. Every rule
// key and value must use that exact family, otherwise construction fails.
func NewStaticMapper(version IPVersion, rules map[Endpoint]Endpoint) (*StaticMapper, error) {
	if version != Version4 && version != Version6 {
		return nil, ErrVersionMismatch
	}
	m := &StaticMapper{version: version, rules: make(map[Endpoint]Endpoint, len(rules))}
	for from, to := range rules {
		from = normalize(from)
		to = normalize(to)
		if from.Version != version || to.Version != version {
			return nil, ErrVersionMismatch
		}
		if !validAddr(version, from.Addr) || !validAddr(version, to.Addr) {
			return nil, ErrVersionMismatch
		}
		m.rules[from] = to
	}
	return m, nil
}

// Version reports the single address family this mapper handles.
func (m *StaticMapper) Version() IPVersion { return m.version }

// Translate applies the fixed table. An endpoint without a rule is returned
// unchanged. Endpoints of another family are rejected.
func (m *StaticMapper) Translate(ep Endpoint) (Endpoint, error) {
	if ep.Version != m.version {
		return Endpoint{}, ErrVersionMismatch
	}
	ep = normalize(ep)
	if mapped, ok := m.rules[ep]; ok {
		return mapped, nil
	}
	return ep, nil
}

// IdentityMapper performs no translation but still enforces a single fixed
// address family. It is useful when no NAT is in use while keeping the
// fixed-version guarantee on the processing path.
type IdentityMapper struct {
	version IPVersion
}

// NewIdentityMapper creates a pass-through mapper for one fixed family.
func NewIdentityMapper(version IPVersion) (*IdentityMapper, error) {
	if version != Version4 && version != Version6 {
		return nil, ErrVersionMismatch
	}
	return &IdentityMapper{version: version}, nil
}

// Version reports the single address family this mapper handles.
func (m *IdentityMapper) Version() IPVersion { return m.version }

// Translate validates the family and returns the endpoint unchanged.
func (m *IdentityMapper) Translate(ep Endpoint) (Endpoint, error) {
	if ep.Version != m.version {
		return Endpoint{}, ErrVersionMismatch
	}
	return normalize(ep), nil
}

var _ AddrMapper = (*StaticMapper)(nil)
var _ AddrMapper = (*IdentityMapper)(nil)
