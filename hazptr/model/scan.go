package model

import (
	"sort"
	"strings"
)

// visibleProtection builds the set of (addr,identity) pairs visible from the
// supplied threads' hazard slots. Passing every thread id yields the only
// spec-compliant view.
func (m *Machine) visibleProtection(threadIDs []int) map[hpView]bool {
	protected := map[hpView]bool{}
	for _, id := range threadIDs {
		t := m.threads[id]
		if t == nil {
			continue
		}
		for _, view := range t.visibleHP {
			if view.valid {
				protected[view] = true
			}
		}
	}
	return protected
}

func (m *Machine) allThreadIDs() []int {
	ids := make([]int, 0, len(m.threads))
	for id := range m.threads {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func containsID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func (m *Machine) stepScan(t *thread, op Op) Violation {
	all := m.allThreadIDs()
	scanned := op.IncludeThreads
	if scanned == nil {
		scanned = all
	}

	// A new scan generation starts here. Everything reclaimed by this step
	// is decided from ONE snapshot: retired entries existing at this moment
	// plus the hazard pointers visible at this moment.
	m.scanGen++
	gen := m.scanGen

	candidates := make(map[string]retiredEntry, len(m.retired))
	for addr, entry := range m.retired {
		candidates[addr] = entry
	}

	omitted := []int{}
	for _, id := range all {
		if !containsID(scanned, id) {
			omitted = append(omitted, id)
		}
	}

	if len(omitted) > 0 && m.cfg.EnforceFullScan {
		omittedView := m.visibleProtection(omitted)
		for addr := range candidates {
			for view := range omittedView {
				if view.addr == addr {
					return m.fail(ErrPartialScan, op, addr, view.identity,
						"scan generation %d ignores thread(s) %s whose visible hazard protects %q",
						gen, joinIDs(omitted), addr)
				}
			}
		}
	}

	protected := m.visibleProtection(scanned)
	freed := []string{}
	for addr, entry := range candidates {
		if protectionCovers(protected, addr, entry.identity, m.cfg.TrackIdentity) {
			continue
		}
		o := m.liveObject(addr)
		if o == nil || o.identity != entry.identity {
			continue
		}
		o.freed = true
		o.freedByScan = gen
		delete(m.retired, addr)
		freed = append(freed, addr)
	}

	// Defense-in-depth invariant: no freed address may appear in ANY
	// thread's visible slot. A correct full scan makes this unreachable;
	// the check witnesses premature reclamation when a buggy scan frees an
	// object whose hazard pointer was published too late to be seen in this
	// scan generation, or when a scan simply omitted a thread.
	fullView := m.visibleProtection(all)
	for _, addr := range freed {
		for view := range fullView {
			if view.addr != addr {
				continue
			}
			if m.cfg.TrackIdentity {
				if gen := latestGeneration(m, addr); gen != 0 && gen != view.identity {
					return m.fail(ErrAddressReuse, op, addr, view.identity,
						"scan generation %d left a hazard at reused address %q (stale generation %d, now %d)",
						gen, addr, view.identity, gen)
				}
			}
			return m.fail(ErrPrematureReclaim, op, addr, view.identity,
				"scan generation %d freed %q while a thread hazard still publishes it",
				gen, addr)
		}
	}
	return Violation{}
}

func latestGeneration(m *Machine, addr string) int {
	gens := m.objects[addr]
	if len(gens) == 0 {
		return 0
	}
	return gens[len(gens)-1].identity
}

func protectionCovers(protected map[hpView]bool, addr string, identity int, trackIdentity bool) bool {
	for view := range protected {
		if view.addr != addr {
			continue
		}
		if !trackIdentity || view.identity == identity {
			return true
		}
	}
	return false
}

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "T" + itoa(id)
	}
	return strings.Join(parts, ",")
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (m *Machine) stepAlloc(t *thread, op Op) Violation {
	if op.Reg == "" {
		return infeasible(op, "Alloc requires Reg")
	}
	addr := op.ReuseAddr
	if addr == "" {
		addr = m.pickFreedAddress()
	}
	if addr == "" {
		addr = m.freshAddress()
	}

	// Reuse is only feasible when nothing still names the address.
	if m.liveObject(addr) != nil {
		return infeasible(op, "alloc target %q is still live", addr)
	}
	if _, busy := m.retired[addr]; busy {
		return infeasible(op, "alloc target %q is still retired", addr)
	}
	for _, other := range m.threads {
		if other.visibleRoots[op.Root] == addr {
			return infeasible(op, "alloc target %q still reachable via a root", addr)
		}
		for _, view := range other.visibleHP {
			if view.valid && view.addr == addr {
				return infeasible(op, "alloc target %q still held by a hazard slot", addr)
			}
		}
	}

	m.nextIdent++
	o := m.touchObject(addr, m.nextIdent)
	r := m.reg(t, op.Reg)
	r.addr = o.addr
	r.identity = o.identity
	r.verified = false
	r.verifiedAt = 0
	return Violation{}
}

func (m *Machine) pickFreedAddress() string {
	addrs := make([]string, 0, len(m.objects))
	for addr, gens := range m.objects {
		if len(gens) > 0 && gens[len(gens)-1].freed {
			if _, busy := m.retired[addr]; !busy {
				addrs = append(addrs, addr)
			}
		}
	}
	sort.Strings(addrs)
	if len(addrs) == 0 {
		return ""
	}
	return addrs[0]
}

func (m *Machine) freshAddress() string {
	for {
		m.nextIdent++
		addr := "obj" + itoa(m.nextIdent)
		if _, exists := m.objects[addr]; !exists {
			return addr
		}
	}
}
