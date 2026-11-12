package sctpaddr

import (
	"crypto/rand"
	"net/netip"
	"sort"
	"sync"
)

// Operation names an ASCONF address operation.
type Operation int

const (
	OpAddIP Operation = iota
	OpDeleteIP
	OpSetPrimary
)

// ResultKind classifies the outcome Dispatch reports to the caller.
type ResultKind int

const (
	// KindDropped means the packet was rejected before any state change;
	// Result.Err carries the reason and no response is produced.
	KindDropped ResultKind = iota
	// KindIgnored means the datagram was valid but not relevant to the
	// address manager (for example a DATA-bearing packet).
	KindIgnored
	// KindASCONFACK means an authenticated ASCONF was processed; Result
	// describes the operation and Response is the signed ASCONF ACK.
	KindASCONFACK
	// KindHeartbeatACK means a peer HEARTBEAT was answered; Response is the
	// unsigned HEARTBEAT ACK datagram.
	KindHeartbeatACK
	// KindConfirmed means a HEARTBEAT ACK matched a pending nonce and an
	// address transitioned to reachable/confirmed.
	KindConfirmed
)

// Result is what Dispatch returns to the packet distribution path.
type Result struct {
	Kind     ResultKind
	Op       Operation
	Address  netip.Addr
	Accepted bool
	Cause    uint16
	Err      error
	Response []byte
}

// Config parameterizes a Manager. LocalTag is the verification tag this end
// expects in every non-INIT inbound packet; PeerTag is stamped on outbound
// packets. HMACKey is the RFC 4895 shared key negotiated for the association.
type Config struct {
	LocalPort  uint16
	PeerPort   uint16
	LocalTag   uint32
	PeerTag    uint32
	HMACKey    []byte
	HMACID     uint16
	LocalAddrs []netip.Addr
	Primary    netip.Addr
	NextSN     uint32
	NonceSize  int
}

type addrState struct {
	confirmed bool
}

type pendingProbe struct {
	nonce []byte
}

// Manager owns the multihoming address set of one SCTP association and the
// bookkeeping needed to authenticate ASCONF traffic against it.
type Manager struct {
	mu sync.Mutex

	localPort uint16
	peerPort  uint16
	localTag  uint32
	peerTag   uint32
	hmacKey   []byte
	hmacID    uint16

	addrs     map[netip.Addr]*addrState
	primary   netip.Addr
	expected  uint32
	outbound  uint32
	nonceSize int

	pending map[netip.Addr]*pendingProbe
}

// New validates the configuration and builds a Manager whose initial
// addresses are all treated as confirmed, since they were established during
// the INIT/COOKIE exchange rather than via ASCONF.
func New(cfg Config) (*Manager, error) {
	if len(cfg.HMACKey) == 0 {
		return nil, ErrMalformed
	}
	if cfg.HMACID != 0 {
		if _, err := hashFor(cfg.HMACID); err != nil {
			return nil, err
		}
	} else {
		cfg.HMACID = HMACSHA256
	}
	if cfg.NonceSize <= 0 {
		cfg.NonceSize = 32
	}
	if len(cfg.LocalAddrs) == 0 || !cfg.Primary.IsValid() {
		return nil, ErrMalformed
	}

	m := &Manager{
		localPort: cfg.LocalPort,
		peerPort:  cfg.PeerPort,
		localTag:  cfg.LocalTag,
		peerTag:   cfg.PeerTag,
		hmacKey:   append([]byte(nil), cfg.HMACKey...),
		hmacID:    cfg.HMACID,
		expected:  cfg.NextSN,
		outbound:  cfg.NextSN,
		nonceSize: cfg.NonceSize,
		addrs:     make(map[netip.Addr]*addrState),
		pending:   make(map[netip.Addr]*pendingProbe),
	}
	for _, addr := range cfg.LocalAddrs {
		addr = addr.Unmap()
		if !addr.IsValid() {
			return nil, ErrMalformed
		}
		m.addrs[addr] = &addrState{confirmed: true}
	}
	primary := cfg.Primary.Unmap()
	if _, ok := m.addrs[primary]; !ok {
		return nil, ErrMalformed
	}
	m.primary = primary
	return m, nil
}

// Primary returns the current primary destination address.
func (m *Manager) Primary() netip.Addr {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.primary
}

// Confirmed reports whether addr belongs to the association and has passed a
// HEARTBEAT reachability check.
func (m *Manager) Confirmed(addr netip.Addr) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.addrs[addr.Unmap()]
	return ok && st.confirmed
}

// HasAddr reports whether addr is currently part of the association.
func (m *Manager) HasAddr(addr netip.Addr) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.addrs[addr.Unmap()]
	return ok
}

// Addrs returns a sorted snapshot of the association's addresses.
func (m *Manager) Addrs() []netip.Addr {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]netip.Addr, 0, len(m.addrs))
	for addr := range m.addrs {
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

func (m *Manager) outboundHeader() CommonHeader {
	return CommonHeader{SrcPort: m.localPort, DstPort: m.peerPort, VerificationTag: m.peerTag}
}

// BuildASCONF constructs and signs an outbound ASCONF datagram carrying one
// address operation, advancing the local serial counter.
func (m *Manager) BuildASCONF(op Operation, addr netip.Addr) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var opType uint16
	switch op {
	case OpAddIP:
		opType = ParamASCONFAddIP
	case OpDeleteIP:
		opType = ParamASCONFDeleteIP
	case OpSetPrimary:
		opType = ParamASCONFSetPrimary
	default:
		return nil, ErrUnsupported
	}
	param, err := addressParam(opType, addr.Unmap())
	if err != nil {
		return nil, err
	}
	serial := m.outbound
	m.outbound++
	chunk := buildASCONF(serial, param)
	return signPacket(m.hmacKey, m.hmacID, m.outboundHeader(), []Chunk{chunk})
}

// Probe arms a fresh HEARTBEAT nonce for addr and returns the HEARTBEAT
// datagram to send. HEARTBEAT chunks do not require AUTH (RFC 4895 3.2). The
// address must already be part of the association, for example a newly added
// one awaiting reachability confirmation.
func (m *Manager) Probe(addr netip.Addr) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	addr = addr.Unmap()
	if _, ok := m.addrs[addr]; !ok {
		return nil, ErrUnsupported
	}
	nonce := make([]byte, m.nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	m.pending[addr] = &pendingProbe{nonce: nonce}

	chunk := buildHeartbeat(ChunkHeartbeat, nonce)
	return marshalPacket(m.outboundHeader(), chunk), nil
}
