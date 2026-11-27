package sctpauth

// PathState is the validation status of one peer transport address.
type PathState uint8

const (
	// PathUnconfirmed means the address has been announced (via INIT or ASCONF)
	// but ownership has not been proven with a HEARTBEAT round trip.
	PathUnconfirmed PathState = iota
	// PathConfirmed means an independent nonce challenge was answered from the
	// address, proving packets actually reach and originate there.
	PathConfirmed
)

// String renders a PathState for diagnostics.
func (s PathState) String() string {
	switch s {
	case PathConfirmed:
		return "CONFIRMED"
	default:
		return "UNCONFIRMED"
	}
}

// Path tracks one multihoming destination and its validation challenge.
type Path struct {
	Address      string
	State        PathState
	IsPrimary    bool
	challenge    [nonceSize]byte
	hasChallenge bool
	failCount    uint32
}

// PathManager owns the multihoming gating state machine. Every path starts
// UNCONFIRMED and may only carry user data after a unique, random HEARTBEAT
// challenge has been correctly acknowledged from that exact address.
type PathManager struct {
	keys  *KeyRing
	paths map[string]*Path
}

// NewPathManager builds a manager. By convention the first address is the
// primary path; it still starts UNCONFIRMED unless it is the sole source
// address already proven during the handshake, in which case the host passes
// confirmPrimary.
func NewPathManager(keys *KeyRing, addrs []string, confirmPrimary bool) *PathManager {
	canonical := canonicalAddrSet(addrs)
	manager := &PathManager{
		keys:  keys,
		paths: make(map[string]*Path, len(canonical)),
	}
	for i, addr := range canonical {
		path := &Path{Address: addr, State: PathUnconfirmed, IsPrimary: i == 0}
		manager.paths[addr] = path
	}
	if confirmPrimary && len(canonical) > 0 {
		manager.paths[canonical[0]].State = PathConfirmed
	}
	return manager
}

// Add introduces a path announced by an ASCONF Add-IP chunk. A re-added
// address is forced back to UNCONFIRMED and loses any stale challenge.
func (m *PathManager) Add(addr string) (*Path, error) {
	canonical := canonicalAddr(addr)
	if canonical == "" {
		return nil, ErrInvalidChunk
	}
	path, ok := m.paths[canonical]
	if !ok {
		path = &Path{Address: canonical}
		m.paths[canonical] = path
	}
	path.State = PathUnconfirmed
	path.hasChallenge = false
	path.failCount = 0
	return path, nil
}

// NewChallenge mints an independent random nonce for the given address and
// returns the HEARTBEAT info the host must send only to that address.
func (m *PathManager) NewChallenge(addr string) ([nonceSize]byte, error) {
	path, ok := m.paths[canonicalAddr(addr)]
	if !ok {
		return [nonceSize]byte{}, ErrUnknownPath
	}
	path.challenge = m.keys.Nonce()
	path.hasChallenge = true
	return path.challenge, nil
}

// HandleAck verifies a HEARTBEAT-ACK. The response must originate from the
// challenged address and echo the exact outstanding nonce; otherwise the path
// stays UNCONFIRMED and the failure is counted.
func (m *PathManager) HandleAck(addr string, nonce [nonceSize]byte) error {
	path, ok := m.paths[canonicalAddr(addr)]
	if !ok {
		return ErrUnknownPath
	}
	if !path.hasChallenge || path.challenge != nonce {
		path.failCount++
		return ErrPathUnconfirmed
	}
	path.State = PathConfirmed
	path.hasChallenge = false
	return nil
}

// State reports the current validation state of an address.
func (m *PathManager) State(addr string) (PathState, error) {
	path, ok := m.paths[canonicalAddr(addr)]
	if !ok {
		return PathUnconfirmed, ErrUnknownPath
	}
	return path.State, nil
}

// IsConfirmed reports whether the address may carry user data.
func (m *PathManager) IsConfirmed(addr string) bool {
	state, err := m.State(addr)
	return err == nil && state == PathConfirmed
}

// RequireConfirmed gates outbound user data. It returns ErrPathUnconfirmed for
// any destination that has not completed path validation, which is the
// mechanism that prevents reflection toward third-party IPs.
func (m *PathManager) RequireConfirmed(addr string) error {
	path, ok := m.paths[canonicalAddr(addr)]
	if !ok {
		return ErrUnknownPath
	}
	if path.State != PathConfirmed {
		return ErrPathUnconfirmed
	}
	return nil
}

// Primary returns the primary path address.
func (m *PathManager) Primary() (string, error) {
	for _, path := range m.paths {
		if path.IsPrimary {
			return path.Address, nil
		}
	}
	return "", ErrUnknownPath
}

// Snapshot returns a point-in-time copy of every path.
func (m *PathManager) Snapshot() []Path {
	out := make([]Path, 0, len(m.paths))
	for _, path := range m.paths {
		out = append(out, *path)
	}
	return out
}

// UnconfirmedAddrs lists every address still barred from user data.
func (m *PathManager) UnconfirmedAddrs() []string {
	out := make([]string, 0)
	for addr, path := range m.paths {
		if path.State != PathConfirmed {
			out = append(out, addr)
		}
	}
	return out
}

// FailCount returns the number of failed path proofs for an address.
func (m *PathManager) FailCount(addr string) (uint32, error) {
	path, ok := m.paths[canonicalAddr(addr)]
	if !ok {
		return 0, ErrUnknownPath
	}
	return path.failCount, nil
}
