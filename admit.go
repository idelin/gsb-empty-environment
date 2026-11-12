package netsec

import (
	"net/netip"
	"sync"
)

// Verdict is the admission decision for a single SD entry.
type Verdict uint8

const (
	// VerdictAccept lets the entry reach the SD state machine.
	VerdictAccept Verdict = iota
	// VerdictReject denies a well-formed entry that violates security policy.
	VerdictReject
	// VerdictDrop discards a malformed entry (for example an option index that
	// does not exist) without treating the sender as spoofing.
	VerdictDrop
)

// Reason explains why an entry was rejected or dropped. Accepted entries
// always carry ReasonNone.
type Reason string

const (
	ReasonNone                     Reason = ""
	ReasonInvalidSource            Reason = "invalid packet source address"
	ReasonOptionIndexOutOfBounds   Reason = "entry references option index outside the options array"
	ReasonServiceNotRegistered     Reason = "no provider is registered for service/instance/major version"
	ReasonProviderNotAllowed       Reason = "source address is not an allowed provider for the service tuple"
	ReasonStopOfferFromNonProvider Reason = "StopOffer (TTL 0) did not originate from the current provider"
	ReasonEndpointSourceMismatch   Reason = "endpoint option address differs from the packet source address"
)

// EntryVerdict pairs the per-entry decision with its reason. The slice
// returned by Admit is aligned with Message.Entries by index.
type EntryVerdict struct {
	Verdict Verdict
	Reason  Reason
}

// Controller is the SD admission component called by the gateway's SD message
// handler. It is safe for concurrent use.
//
// The controller holds two kinds of state, both keyed by the
// (ServiceID, InstanceID, MajorVersion) tuple:
//
//   - allowed providers: addresses provisioned by the calling stack through
//     RegisterProvider, i.e. the only addresses permitted to Offer a tuple.
//   - active provider: the address whose Offer most recently passed admission,
//     which is the only address allowed to withdraw it via a StopOffer.
type Controller struct {
	mu      sync.RWMutex
	allowed map[ServiceKey]map[netip.Addr]struct{}
	active  map[ServiceKey]netip.Addr
}

// NewController creates an empty admission controller.
func NewController() *Controller {
	return &Controller{
		allowed: make(map[ServiceKey]map[netip.Addr]struct{}),
		active:  make(map[ServiceKey]netip.Addr),
	}
}

// RegisterProvider provisions addr as an allowed provider of key. Offers for
// the tuple are only accepted from registered addresses.
func (c *Controller) RegisterProvider(key ServiceKey, addr netip.Addr) {
	addr = addr.Unmap()
	c.mu.Lock()
	defer c.mu.Unlock()
	addrs, ok := c.allowed[key]
	if !ok {
		addrs = make(map[netip.Addr]struct{})
		c.allowed[key] = addrs
	}
	addrs[addr] = struct{}{}
}

// UnregisterProvider removes addr from the allowed providers of key. It does
// not clear the active provider; use RevokeActiveProvider for that.
func (c *Controller) UnregisterProvider(key ServiceKey, addr netip.Addr) {
	addr = addr.Unmap()
	c.mu.Lock()
	defer c.mu.Unlock()
	if addrs, ok := c.allowed[key]; ok {
		delete(addrs, addr)
		if len(addrs) == 0 {
			delete(c.allowed, key)
		}
	}
}

// CurrentProvider reports the address whose Offer currently owns the tuple.
func (c *Controller) CurrentProvider(key ServiceKey) (netip.Addr, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	addr, ok := c.active[key]
	return addr, ok
}

// RevokeActiveProvider clears the active-provider record for key. It is meant
// for housekeeping (TTL expiry, interface down); admission of StopOffers is
// handled automatically by Admit.
func (c *Controller) RevokeActiveProvider(key ServiceKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, key)
}

// AdmitPacket parses a raw SOME/IP-SD datagram and applies the admission
// policy using the datagram's transport source address. Parse errors are
// returned so the caller can count malformed traffic; no state is changed.
func (c *Controller) AdmitPacket(packet []byte, source netip.Addr) ([]EntryVerdict, error) {
	msg, err := ParseMessage(packet)
	if err != nil {
		return nil, err
	}
	return c.Admit(msg, source), nil
}

// Admit applies the admission policy to every entry of a parsed SD message.
// The returned slice has one EntryVerdict per entry, in message order.
func (c *Controller) Admit(msg *Message, source netip.Addr) []EntryVerdict {
	source = source.Unmap()
	c.mu.Lock()
	defer c.mu.Unlock()

	verdicts := make([]EntryVerdict, len(msg.Entries))
	for i, entry := range msg.Entries {
		verdicts[i] = c.admitEntry(entry, msg.Options, source)
	}
	return verdicts
}

func (c *Controller) admitEntry(entry Entry, options []Option, source netip.Addr) EntryVerdict {
	if !source.IsValid() {
		return reject(ReasonInvalidSource)
	}
	// Any entry referencing a non-existent option is discarded outright,
	// regardless of type, so a crafted index can never reach later checks.
	if !runInBounds(entry.Index1, entry.Num1, len(options)) ||
		!runInBounds(entry.Index2, entry.Num2, len(options)) {
		return drop(ReasonOptionIndexOutOfBounds)
	}

	switch entry.Type {
	case EntryOfferService:
		return c.admitOffer(entry, options, source)
	case EntrySubscribe:
		return c.admitSubscribe(entry, options, source)
	default:
		// Find, SubscribeAck and unknown entries carry no local state change;
		// bounds validation above is sufficient.
		return accept()
	}
}

func (c *Controller) admitOffer(entry Entry, options []Option, source netip.Addr) EntryVerdict {
	key := ServiceKey{entry.ServiceID, entry.InstanceID, entry.MajorVersion}
	if entry.IsStopOffer() {
		// A withdraw only makes sense when it comes from the address that
		// currently owns the tuple; check this before the static registry so a
		// forged StopOffer is classified correctly even if its sender was
		// provisioned at some point but never held the active offer.
		current, ok := c.active[key]
		if !ok || current != source {
			return reject(ReasonStopOfferFromNonProvider)
		}
		if _, allowed := c.allowed[key][source]; !allowed {
			return reject(ReasonProviderNotAllowed)
		}
		delete(c.active, key)
		return accept()
	}

	addrs, registered := c.allowed[key]
	if !registered {
		return reject(ReasonServiceNotRegistered)
	}
	if _, ok := addrs[source]; !ok {
		return reject(ReasonProviderNotAllowed)
	}
	// Defense in depth: the offer's own unicast endpoint must not advertise a
	// different address than the one it was sent from. Multicast options are
	// not checked because they legitimately name a group address.
	if !endpointMatchesSource(entry.Index1, entry.Num1, options, source) {
		return reject(ReasonEndpointSourceMismatch)
	}

	c.active[key] = source
	return accept()
}

func (c *Controller) admitSubscribe(entry Entry, options []Option, source netip.Addr) EntryVerdict {
	// The subscriber's unicast endpoint must equal the packet source,
	// otherwise the attacker is redirecting event traffic at a victim.
	if !endpointMatchesSource(entry.Index1, entry.Num1, options, source) {
		return reject(ReasonEndpointSourceMismatch)
	}
	return accept()
}

// runInBounds reports whether an option run [index, index+num) lies fully
// inside an options array of n entries. An empty run never references an
// option and is always in bounds.
func runInBounds(index, num uint8, n int) bool {
	if num == 0 {
		return true
	}
	start := int(index)
	end := start + int(num)
	if end < start {
		return false
	}
	return start < n && end <= n
}

// endpointMatchesSource verifies that every unicast endpoint (or SD endpoint)
// option in the run carries source as its address. Configuration, load
// balancing and multicast options are not constrained by this check.
func endpointMatchesSource(index, num uint8, options []Option, source netip.Addr) bool {
	for i := int(index); i < int(index)+int(num); i++ {
		switch options[i].Type {
		case OptionIPv4Endpoint, OptionIPv6Endpoint,
			OptionIPv4SDEndpoint, OptionIPv6SDEndpoint:
			if options[i].Address != source {
				return false
			}
		}
	}
	return true
}

func accept() EntryVerdict         { return EntryVerdict{Verdict: VerdictAccept} }
func reject(r Reason) EntryVerdict { return EntryVerdict{Verdict: VerdictReject, Reason: r} }
func drop(r Reason) EntryVerdict   { return EntryVerdict{Verdict: VerdictDrop, Reason: r} }
