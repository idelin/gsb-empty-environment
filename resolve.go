package netsec

// Resolve walks the forwarding chain of ref within one fixed version graph and
// returns the final concrete host address together with every module identity
// traversed.
//
// Security properties:
//   - Identity is bound to (name, digest): the same name with a different
//     digest is a different module and can never satisfy the reference.
//   - Authorization is checked at every hop, not only at the first hop. A
//     single unauthorized module anywhere on the chain rejects the resolution,
//     so an unauthorized target cannot hide behind an authorized forwarder.
//   - Reachable forwarding cycles are rejected.
//   - Repeated resolutions share a per-generation cache; cached failures are
//     returned as well, so poisoning one entry cannot affect other references.
func (l *Loader) Resolve(ref Ref) (Resolved, error) {
	if !ref.valid() {
		return Resolved{}, fail("resolve", ref, ErrInvalidRef)
	}
	g := l.snapshot()
	gray := make(map[Ref]bool)
	trail := make([]ModuleID, 0, 4)
	addr, err := g.resolve(ref, gray, &trail)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Addr: addr, Trail: append([]ModuleID(nil), trail...)}, nil
}

// resolve performs the depth-first walk. gray holds references on the current
// DFS path for cycle detection; trail accumulates modules in visit order.
func (g *graph) resolve(ref Ref, gray map[Ref]bool, trail *[]ModuleID) (Address, error) {
	g.mu.RLock()
	cached, ok := g.cache[ref]
	g.mu.RUnlock()
	if ok {
		*trail = append(*trail, cached.trail...)
		return cached.addr, cached.err
	}

	if gray[ref] {
		return 0, fail("resolve", ref, ErrCycle).withTrail(*trail)
	}

	st, known := g.modules[ref.Module]
	if !known {
		err := fail("resolve", ref, ErrUnknownModule).withTrail(*trail)
		g.fail(ref, *trail, err)
		return 0, err
	}
	if !st.authorized {
		err := fail("resolve", ref, ErrUnauthorized).withTrail(*trail)
		g.fail(ref, *trail, err)
		return 0, err
	}

	g.mu.Lock()
	binding, ok := g.bindings[ref]
	if ok {
		g.lookups++
	}
	g.mu.Unlock()
	if !ok {
		err := fail("resolve", ref, ErrUnknownSymbol).withTrail(*trail)
		g.fail(ref, *trail, err)
		return 0, err
	}

	gray[ref] = true
	*trail = append(*trail, ref.Module)

	if binding.Concrete {
		delete(gray, ref)
		g.succeed(ref, *trail, binding.Addr)
		return binding.Addr, nil
	}

	addr, err := g.resolve(binding.To, gray, trail)
	delete(gray, ref)
	if err != nil {
		return 0, err
	}
	g.succeed(ref, *trail, addr)
	return addr, nil
}

func (g *graph) succeed(ref Ref, trail []ModuleID, addr Address) {
	g.mu.Lock()
	g.cache[ref] = cacheEntry{addr: addr, trail: append([]ModuleID(nil), trail...)}
	g.mu.Unlock()
}

func (g *graph) fail(ref Ref, trail []ModuleID, err error) {
	g.mu.Lock()
	g.cache[ref] = cacheEntry{err: err, trail: append([]ModuleID(nil), trail...)}
	g.mu.Unlock()
}

// cacheLen reports the number of cached resolution entries. It is intended for
// tests that verify cache sharing.
func (g *graph) cacheLen() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.cache)
}

// lookupCount reports how many bindings were fetched outside the cache. It is
// intended for tests that verify repeated resolutions share the cache.
func (g *graph) lookupCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.lookups
}

// cacheStats exposes cache statistics for the currently installed generation.
func (l *Loader) cacheStats() (entries, lookups int) {
	g := l.snapshot()
	return g.cacheLen(), g.lookupCount()
}
