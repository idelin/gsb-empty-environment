package nat64

import "net/netip"

// lookupBIBV6 returns the EIM binding for an IPv6 (address, proto, port).
func (e *Engine) lookupBIBV6(proto uint8, src6 netip.Addr, port uint16) *BIBEntry {
	return e.bibV6[BIBV6Key{Addr: src6, Proto: proto, Port: port}]
}

// lookupBIBV4 returns the binding targeted by an IPv4 packet.
func (e *Engine) lookupBIBV4(proto uint8, poolAddr netip.Addr, port uint16) *BIBEntry {
	return e.bibV4[BIBV4Key{Addr: poolAddr, Proto: proto, Port: port}]
}

// poolCount counts current BIB entries per pool address.
func (e *Engine) poolCount(addr netip.Addr) int {
	n := 0
	for _, b := range e.bibV6 {
		if b.V4Addr == addr {
			n++
		}
	}
	return n
}

// choosePool picks the least-loaded pool address; ties resolve in
// configuration order so allocation stays deterministic.
func (e *Engine) choosePool() netip.Addr {
	best := e.cfg.PoolV4[0]
	bestCount := e.poolCount(best)
	for _, a := range e.cfg.PoolV4[1:] {
		if c := e.poolCount(a); c < bestCount {
			best, bestCount = a, c
		}
	}
	return best
}

// allocateBIB creates an EIM binding. Allocation preserves the source port
// range (system 0..1023 vs user 1024..65535) and tries, in order:
//
//  1. the exact source port;
//  2. a free port with the same parity inside the same range;
//  3. any free port inside the same range;
//  4. (only for user-range sources) any free pool port.
//
// This implements RFC 6146 section 4.2.2's parity/range preservation.
func (e *Engine) allocateBIB(proto uint8, src6 netip.Addr, srcPort uint16) (*BIBEntry, bool) {
	system := srcPort <= 1023
	low, high := uint16(1024), uint16(65535)
	if system {
		low, high = 0, 1023
	}

	pool := e.choosePool()

	candidates := make([]uint16, 0, 4)
	if p, ok := e.firstFree(pool, proto, srcPort, srcPort); ok {
		candidates = append(candidates, p)
	}
	if p, ok := e.firstFreeParity(pool, proto, low, high, srcPort%2 == 0); ok && p != srcPort {
		candidates = append(candidates, p)
	}
	if p, ok := e.firstFree(pool, proto, low, high); ok && p != srcPort && p%2 != srcPort%2 {
		candidates = append(candidates, p)
	}
	if !system {
		if p, ok := e.firstFree(pool, proto, 0, 1023); ok {
			candidates = append(candidates, p)
		}
	}

	for _, port := range candidates {
		if e.portInUse(pool, proto, port) {
			continue
		}
		now := e.now()
		b := &BIBEntry{
			Proto:    proto,
			V6Addr:   src6,
			V6Port:   srcPort,
			V4Addr:   pool,
			V4Port:   port,
			Created:  now,
			LastUsed: now,
		}
		e.bibV6[BIBV6Key{Addr: src6, Proto: proto, Port: srcPort}] = b
		e.bibV4[BIBV4Key{Addr: pool, Proto: proto, Port: port}] = b
		e.stats.BIBCreated++
		return b, true
	}
	return nil, false
}

func (e *Engine) portInUse(addr netip.Addr, proto uint8, port uint16) bool {
	_, ok := e.bibV4[BIBV4Key{Addr: addr, Proto: proto, Port: port}]
	return ok
}

// firstFree returns the first unallocated port in [lo, hi].
func (e *Engine) firstFree(addr netip.Addr, proto uint8, lo, hi uint16) (uint16, bool) {
	for p := int(lo); p <= int(hi); p++ {
		if !e.portInUse(addr, proto, uint16(p)) {
			return uint16(p), true
		}
	}
	return 0, false
}

// firstFreeParity scans from low to high, skipping ports whose parity does
// not match wantEven.
func (e *Engine) firstFreeParity(addr netip.Addr, proto uint8, lo, hi uint16, wantEven bool) (uint16, bool) {
	p := int(lo)
	if wantEven != (p%2 == 0) {
		p++
	}
	for ; p <= int(hi); p += 2 {
		port := uint16(p)
		if !e.portInUse(addr, proto, port) {
			return port, true
		}
	}
	return 0, false
}

// holdBIB increments the reference count of a binding.
func (e *Engine) holdBIB(b *BIBEntry) { b.Refs++ }

// releaseBIB decrements the reference count and removes the binding once no
// session references it anymore (RFC 6146 section 4.4).
func (e *Engine) releaseBIB(b *BIBEntry) {
	if b == nil {
		return
	}
	b.Refs--
	if b.Refs > 0 {
		return
	}
	delete(e.bibV6, BIBV6Key{Addr: b.V6Addr, Proto: b.Proto, Port: b.V6Port})
	delete(e.bibV4, BIBV4Key{Addr: b.V4Addr, Proto: b.Proto, Port: b.V4Port})
}
