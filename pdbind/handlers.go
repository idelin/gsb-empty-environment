package pdbind

import (
	"net/netip"
	"time"
)

// validateRelay enforces the relay chain depth limit and address checks.
// Every hop must carry a usable peer address; the innermost hop's link
// address must be specified and, when AllowedLinks is configured, fall inside
// one of the allowed link ranges.
func (m *Manager) validateRelay(relay []RelayHop) error {
	if len(relay) > m.cfg.MaxRelayDepth {
		return ErrRelayDepth
	}
	for _, hop := range relay {
		if !hop.PeerAddr.IsValid() || !hop.PeerAddr.Is6() || hop.PeerAddr.IsUnspecified() {
			return ErrInvalidLink
		}
		if !hop.LinkAddr.IsValid() || !hop.LinkAddr.Is6() {
			return ErrInvalidLink
		}
	}
	if len(relay) == 0 {
		return nil
	}
	inner := relay[0]
	if inner.LinkAddr.IsUnspecified() {
		return ErrInvalidLink
	}
	if len(m.cfg.AllowedLinks) > 0 {
		allowed := false
		for _, link := range m.cfg.AllowedLinks {
			if link.Contains(inner.LinkAddr) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrLinkNotAllowed
		}
	}
	return nil
}

// linkAddrOf returns the link address on which the message arrived: the
// innermost relay link, or the unspecified address for direct messages.
func linkAddrOf(relay []RelayHop) netip.Addr {
	if len(relay) == 0 {
		return netip.IPv6Unspecified()
	}
	return relay[0].LinkAddr
}

// nextHopOf returns the address to which traffic for the delegated prefix is
// forwarded: the innermost relay peer for relayed messages, otherwise the
// client's own address.
func nextHopOf(msg ClientMessage, relay []RelayHop) netip.Addr {
	if len(relay) > 0 {
		return relay[0].PeerAddr
	}
	return msg.PeerAddr
}

// lookupLocked returns the current binding for key after lazily purging an
// expired one. Caller must hold m.mu.
func (m *Manager) lookupLocked(key BindingKey) (*Binding, bool) {
	binding, ok := m.binds[key]
	if !ok {
		return nil, false
	}
	if !binding.ValidUntil.After(m.cfg.Now()) {
		delete(m.binds, key)
		delete(m.owners, m.cfg.Pool.indexOf(binding.Prefix))
		return nil, false
	}
	return binding, true
}

// pickPrefix honours the client hint only when it is an aligned, free prefix
// inside the pool; otherwise it allocates the first unused pool prefix.
// Prefixes staged by earlier IA_PDs in the same decision are also treated as
// occupied so one message cannot hand the same prefix out twice.
func (m *Manager) pickPrefix(hint *PrefixLease, d *Decision) (netip.Prefix, bool) {
	if err := m.cfg.Pool.Validate(); err != nil {
		return netip.Prefix{}, false
	}
	if hint != nil && m.cfg.Pool.contains(hint.Prefix) {
		hintIdx := m.cfg.Pool.indexOf(hint.Prefix)
		if _, taken := m.owners[hintIdx]; !taken && !d.usesIndex(hintIdx) {
			return hint.Prefix, true
		}
	}
	used := make(map[uint64]bool, len(m.owners))
	for idx := range m.owners {
		used[idx] = true
	}
	for _, binding := range d.upserts {
		used[m.cfg.Pool.indexOf(binding.Prefix)] = true
	}
	return m.cfg.Pool.alloc(used)
}

func (m *Manager) newBinding(key BindingKey, duid []byte, iaid uint32,
	prefix netip.Prefix, link, nextHop netip.Addr, now time.Time) *Binding {
	return &Binding{
		Key:          key,
		DUID:         append([]byte(nil), duid...),
		IAID:         iaid,
		Prefix:       prefix.Masked(),
		LinkAddr:     link,
		NextHop:      nextHop,
		PreferredLft: m.cfg.PreferredLft,
		ValidLft:     m.cfg.ValidLft,
		PreferredAt:  now.Add(time.Duration(m.cfg.PreferredLft) * time.Second),
		ValidUntil:   now.Add(time.Duration(m.cfg.ValidLft) * time.Second),
	}
}

func (m *Manager) refreshBinding(binding *Binding, nextHop netip.Addr, now time.Time) {
	if nextHop.IsValid() {
		binding.NextHop = nextHop
	}
	binding.PreferredLft = m.cfg.PreferredLft
	binding.ValidLft = m.cfg.ValidLft
	binding.PreferredAt = now.Add(time.Duration(m.cfg.PreferredLft) * time.Second)
	binding.ValidUntil = now.Add(time.Duration(m.cfg.ValidLft) * time.Second)
}

// messageMatches reports whether the prefix carried in a message equals the
// stored binding prefix (and, for relayed traffic, the link matches too).
func (m *Manager) messageMatches(binding *Binding, ia IAPD, link netip.Addr) bool {
	if ia.Hint == nil || binding.Prefix != ia.Hint.Prefix {
		return false
	}
	if link.IsValid() && !link.IsUnspecified() &&
		binding.LinkAddr.IsValid() && !binding.LinkAddr.IsUnspecified() &&
		binding.LinkAddr != link {
		return false
	}
	return true
}

func (m *Manager) assign(msg ClientMessage, relay []RelayHop, d *Decision) {
	now := m.cfg.Now()
	link := linkAddrOf(relay)
	nextHop := nextHopOf(msg, relay)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ia := range msg.IAPDs {
		key := MakeKey(msg.DUID, ia.IAID)
		binding, ok := m.lookupLocked(key)
		if ok {
			// The existing binding belongs to this DUID+IAID. The client
			// hint cannot make it take somebody else's prefix: the stored
			// prefix always wins and must stay on its original link.
			if link.IsValid() && binding.LinkAddr.IsValid() &&
				!binding.LinkAddr.IsUnspecified() && binding.LinkAddr != link {
				d.Status = StatusNoBinding
				d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID})
				continue
			}
			m.refreshBinding(binding, nextHop, now)
			d.upserts[key] = binding
			d.routes = append(d.routes, stagedRoute{
				install: true,
				route:   Route{Prefix: binding.Prefix, NextHop: binding.NextHop, Link: binding.LinkAddr},
			})
			d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID, Hint: binding.Lease()})
			continue
		}

		prefix, ok := m.pickPrefix(ia.Hint, d)
		if !ok {
			d.Status = StatusNoPrefixAvail
			d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID})
			continue
		}
		binding = m.newBinding(key, msg.DUID, ia.IAID, prefix, link, nextHop, now)
		d.upserts[key] = binding
		d.routes = append(d.routes, stagedRoute{
			install: true,
			route:   Route{Prefix: prefix, NextHop: nextHop, Link: link},
		})
		d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID, Hint: binding.Lease()})
	}
}

func (m *Manager) renew(msg ClientMessage, relay []RelayHop, d *Decision) {
	now := m.cfg.Now()
	nextHop := nextHopOf(msg, relay)
	link := linkAddrOf(relay)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ia := range msg.IAPDs {
		key := MakeKey(msg.DUID, ia.IAID)
		binding, ok := m.lookupLocked(key)
		if !ok || !m.messageMatches(binding, ia, link) {
			d.Status = StatusNoBinding
			d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID})
			continue
		}
		m.refreshBinding(binding, nextHop, now)
		d.upserts[key] = binding
		d.routes = append(d.routes, stagedRoute{
			install: true,
			route:   Route{Prefix: binding.Prefix, NextHop: binding.NextHop, Link: binding.LinkAddr},
		})
		d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID, Hint: binding.Lease()})
	}
}

func (m *Manager) rebind(msg ClientMessage, relay []RelayHop, d *Decision) {
	now := m.cfg.Now()
	nextHop := nextHopOf(msg, relay)
	link := linkAddrOf(relay)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ia := range msg.IAPDs {
		key := MakeKey(msg.DUID, ia.IAID)
		binding, ok := m.lookupLocked(key)
		if !ok || !m.messageMatches(binding, ia, link) {
			d.Status = StatusNoBinding
			d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID})
			continue
		}
		m.refreshBinding(binding, nextHop, now)
		d.upserts[key] = binding
		d.routes = append(d.routes, stagedRoute{
			install: true,
			route:   Route{Prefix: binding.Prefix, NextHop: binding.NextHop, Link: binding.LinkAddr},
		})
		d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID, Hint: binding.Lease()})
	}
}

func (m *Manager) release(msg ClientMessage, relay []RelayHop, d *Decision) {
	link := linkAddrOf(relay)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ia := range msg.IAPDs {
		key := MakeKey(msg.DUID, ia.IAID)
		binding, ok := m.lookupLocked(key)
		if !ok || !m.messageMatches(binding, ia, link) {
			d.Status = StatusNoBinding
			d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID})
			continue
		}
		d.deletes[key] = binding.Prefix
		d.routes = append(d.routes, stagedRoute{remove: binding.Prefix})
		d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID, Hint: binding.Lease()})
	}
}

func (m *Manager) confirm(msg ClientMessage, d *Decision) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ia := range msg.IAPDs {
		key := MakeKey(msg.DUID, ia.IAID)
		binding, ok := m.lookupLocked(key)
		if !ok || ia.Hint == nil || binding.Prefix != ia.Hint.Prefix {
			d.Status = StatusNoBinding
			d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID})
			continue
		}
		d.IAPDs = append(d.IAPDs, IAPD{IAID: ia.IAID, Hint: binding.Lease()})
	}
}
