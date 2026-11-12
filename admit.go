package netsec

import (
	"net/netip"
	"time"
)

// Reject reasons reported in Decision.Reason. The empty string means the
// response was accepted.
const (
	ReasonOK                 = ""
	ReasonInvalidPacket      = "invalid packet metadata"
	ReasonBlockedName        = "name is forbidden over multicast (wpad/isatap)"
	ReasonMDNSSourcePort     = "mDNS response not from UDP port 5353"
	ReasonMDNSPacketTTL      = "mDNS response IP TTL/hop limit is not 255"
	ReasonMDNSOffLink        = "mDNS response source is not on a local prefix"
	ReasonMDNSNotLocal       = "mDNS response name is not under .local"
	ReasonLLMNRSourcePort    = "LLMNR response not from UDP port 5355"
	ReasonLLMNRNoTransaction = "LLMNR response matches no outstanding query"
	ReasonLLMNROffLink       = "LLMNR response source is not on the local link"
)

// LinkConfig describes the local links an endpoint is attached to. It is
// populated by the socket layer from the host's interface configuration.
type LinkConfig struct {
	// Prefixes are the assigned on-link IP prefixes (interface routes).
	Prefixes []netip.Prefix
}

// onLink reports whether addr belongs to a configured on-link prefix or to a
// well-known link-local range. Link-local addresses are always on-link by
// definition (169.254.0.0/16 for IPv4, fe80::/10 for IPv6), which matters
// for hosts that only have an autoconfigured address on a segment.
func (c LinkConfig) onLink(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return false
	}
	if addr.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range c.Prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// PacketMeta carries the socket-layer envelope facts that admission needs
// but that are not part of the DNS message itself.
type PacketMeta struct {
	Proto      Protocol
	SourceIP   netip.Addr
	SourcePort uint16
	// IPTTL is the IPv4 TTL or IPv6 hop limit observed on the packet.
	IPTTL       uint8
	InterfaceID uint32 // interface index the packet arrived on; 0 = unknown
}

// PendingQuery is an outstanding multicast question awaiting a response.
type PendingQuery struct {
	Proto       Protocol
	ID          uint16
	Name        string
	InterfaceID uint32 // interface the query was sent on; 0 = unknown
	ExpiresAt   time.Time
}

// Decision is the result of admitting (or rejecting) one response packet.
type Decision struct {
	Accepted bool
	Reason   string
	// Cached is the subset of the message's records that passed relevance
	// filtering and were actually written to the cache. It is empty for
	// rejected packets.
	Cached []Record
}

// Admitter is the multicast response admission entry point. It is safe for
// concurrent use because resolver goroutines may call Admit while the
// cache is being read.
type Admitter struct {
	mu      chan struct{}
	links   LinkConfig
	pending map[llmnrKey]PendingQuery
	cache   map[string]map[string]cacheEntry
	now     func() time.Time
}

type llmnrKey struct {
	id  uint16
	key string // normalized question name
}

// NewAdmitter builds an Admitter whose on-link view is links. The clock is
// injected so cache expiry and pending-query timeouts stay deterministic in
// tests; pass time.Now in production.
func NewAdmitter(links LinkConfig, now func() time.Time) *Admitter {
	if now == nil {
		now = time.Now
	}
	return &Admitter{
		mu:      make(chan struct{}, 1),
		links:   links,
		pending: make(map[llmnrKey]PendingQuery),
		cache:   make(map[string]map[string]cacheEntry),
		now:     now,
	}
}

func (a *Admitter) lock()   { a.mu <- struct{}{} }
func (a *Admitter) unlock() { <-a.mu }

// RegisterPending records an outstanding multicast query so that a later
// LLMNR response can be correlated by transaction ID.
func (a *Admitter) RegisterPending(q PendingQuery) {
	q.Name = normalizeName(q.Name)
	a.lock()
	defer a.unlock()
	a.pending[llmnrKey{id: q.ID, key: q.Name}] = q
}

// DropPending removes an outstanding query, for example when the caller
// gives up waiting and times the query out itself.
func (a *Admitter) DropPending(proto Protocol, id uint16, name string) {
	a.lock()
	defer a.unlock()
	delete(a.pending, llmnrKey{id: id, key: normalizeName(name)})
}

// Admit is the entry point the socket layer calls for every received
// multicast response. It validates the envelope against protocol-specific
// rules, enforces the wpad/isatap multicast ban, filters irrelevant
// additional-section records, and caches only what survived.
func (a *Admitter) Admit(meta PacketMeta, msg *Message) Decision {
	if msg == nil || !meta.SourceIP.IsValid() || meta.SourceIP.IsMulticast() {
		return Decision{Accepted: false, Reason: ReasonInvalidPacket}
	}

	// Policy name ban first, so a wpad/isatap answer never benefits from a
	// permissive envelope and never reaches the cache.
	for _, name := range allNames(msg) {
		if isBlockedName(name) {
			return Decision{Accepted: false, Reason: ReasonBlockedName}
		}
	}

	switch meta.Proto {
	case MDNS:
		if reason := a.admitMDNS(meta, msg); reason != ReasonOK {
			return Decision{Accepted: false, Reason: reason}
		}
	case LLMNR:
		if reason := a.admitLLMNR(meta, msg); reason != ReasonOK {
			return Decision{Accepted: false, Reason: reason}
		}
	default:
		return Decision{Accepted: false, Reason: ReasonInvalidPacket}
	}

	cached := a.store(selectCacheable(msg), a.now())
	return Decision{Accepted: true, Reason: ReasonOK, Cached: cached}
}

// admitMDNS enforces the four mDNS acceptance conditions:
// source port 5353, IP TTL/hop limit 255, on-link source, .local names.
func (a *Admitter) admitMDNS(meta PacketMeta, msg *Message) string {
	if meta.SourcePort != MDNSPort {
		return ReasonMDNSSourcePort
	}
	// A genuine on-link mDNS sender cannot be routed, so its datagrams
	// arrive with the full initial TTL of 255. Any lower value proves a
	// router hop, i.e. a remote attacker.
	if meta.IPTTL != MDNSTTL {
		return ReasonMDNSPacketTTL
	}
	if !a.links.onLink(meta.SourceIP) {
		return ReasonMDNSOffLink
	}
	for _, name := range allNames(msg) {
		if !isLocalName(name) {
			return ReasonMDNSNotLocal
		}
	}
	return ReasonOK
}

// admitLLMNR enforces the LLMNR acceptance conditions: the response must
// come from the LLMNR port, be on-link, and match a still-outstanding
// query's transaction ID and question name.
func (a *Admitter) admitLLMNR(meta PacketMeta, msg *Message) string {
	if meta.SourcePort != LLMNRPort {
		return ReasonLLMNRSourcePort
	}
	if !a.links.onLink(meta.SourceIP) {
		return ReasonLLMNROffLink
	}

	a.lock()
	defer a.unlock()

	now := a.now()
	for k, q := range a.pending {
		if !q.ExpiresAt.IsZero() && now.After(q.ExpiresAt) {
			delete(a.pending, k)
		}
	}

	var matched llmnrKey
	found := false
	for i := range msg.Questions {
		k := llmnrKey{id: msg.ID, key: normalizeName(msg.Questions[i].Name)}
		q, ok := a.pending[k]
		if !ok {
			continue
		}
		if q.InterfaceID != 0 && meta.InterfaceID != 0 && q.InterfaceID != meta.InterfaceID {
			continue
		}
		matched, found = k, true
		break
	}
	if !found {
		return ReasonLLMNRNoTransaction
	}
	// One transaction may be answered exactly once; consuming it makes a
	// replayed or duplicate response fail the same check.
	delete(a.pending, matched)
	return ReasonOK
}
