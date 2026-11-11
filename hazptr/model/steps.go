package model

func (m *Machine) stepSetRoot(t *thread, op Op) Violation {
	if op.Root == "" {
		return infeasible(op, "SetRoot requires Root")
	}
	if op.Addr != "" && m.liveObject(op.Addr) == nil {
		return infeasible(op, "SetRoot on non-live address %q", op.Addr)
	}
	t.stagedRoots[op.Root] = op.Addr
	return Violation{}
}

func (m *Machine) stepFlushRoot(t *thread, _ Op) Violation {
	for root, addr := range t.stagedRoots {
		t.visibleRoots[root] = addr
	}
	t.stagedRoots = map[string]string{}
	return Violation{}
}

func (m *Machine) stepLoadRoot(t *thread, op Op) Violation {
	if op.Root == "" || op.Reg == "" {
		return infeasible(op, "LoadRoot requires Root and Reg")
	}
	addr := t.visibleRoots[op.Root]
	r := m.reg(t, op.Reg)
	r.addr = addr
	r.identity = 0
	if addr != "" {
		if o := m.liveObject(addr); o != nil {
			r.identity = o.identity
		}
	}
	// A fresh load invalidates any prior verification.
	r.verified = false
	r.verifiedAt = 0
	return Violation{}
}

func (m *Machine) stepProtect(t *thread, op Op) Violation {
	if op.Addr == "" {
		return infeasible(op, "Protect requires Addr")
	}
	o := m.liveObject(op.Addr)
	if o == nil {
		return infeasible(op, "Protect on non-live address %q", op.Addr)
	}
	// Staged locally: another thread cannot observe it before FlushHP.
	t.stagedHP[op.Slot] = hpView{
		addr:     op.Addr,
		identity: o.identity,
		valid:    true,
	}
	return Violation{}
}

func (m *Machine) stepFlushHP(t *thread, op Op) Violation {
	for slot, view := range t.stagedHP {
		if view.valid {
			t.visibleHP[slot] = view
		} else {
			delete(t.visibleHP, slot)
		}
	}
	t.stagedHP = map[int]hpView{}
	return Violation{}
}

func (m *Machine) stepClearHP(t *thread, op Op) Violation {
	t.stagedHP[op.Slot] = hpView{valid: false}
	return Violation{}
}

func (m *Machine) stepVerifyRoot(t *thread, op Op) Violation {
	if op.Root == "" || op.Reg == "" {
		return infeasible(op, "VerifyRoot requires Root and Reg")
	}
	r := m.reg(t, op.Reg)
	if r.addr == "" {
		return infeasible(op, "register %q was never loaded", op.Reg)
	}
	seen := t.visibleRoots[op.Root]
	if seen != r.addr || seen == "" {
		// Relaxed re-read saw a different (or still-unpublished) root:
		// a correct reader aborts and retries. This is not a violation.
		t.aborted = true
		return Violation{}
	}
	if m.liveObject(r.addr) == nil {
		// The pointer is still installed but the object was reclaimed in
		// another scan generation; a real reader cannot make progress and
		// must restart rather than touch freed memory.
		t.aborted = true
		return Violation{}
	}

	// The re-read is consistent. The protection must additionally be
	// visible and, in tagged mode, still name the same object generation.
	protected := false
	for _, view := range t.visibleHP {
		if !view.valid || view.addr != r.addr {
			continue
		}
		if m.cfg.TrackIdentity && view.identity != r.identity {
			continue
		}
		protected = true
		break
	}
	if !protected {
		if m.cfg.TrackIdentity && m.protectsDifferentGeneration(t, r.addr) {
			return m.fail(ErrAddressReuse, op, r.addr, r.identity,
				"re-read matches address %q but visible hazard names a reused generation", r.addr)
		}
		t.aborted = true
		return Violation{}
	}
	r.verified = true
	r.verifiedAt = m.step
	return Violation{}
}

func (m *Machine) protectsDifferentGeneration(t *thread, addr string) bool {
	for _, view := range t.visibleHP {
		if view.valid && view.addr == addr {
			return true
		}
	}
	return false
}

func (m *Machine) stepDeref(t *thread, op Op) Violation {
	if op.Reg == "" {
		return infeasible(op, "Deref requires Reg")
	}
	r := m.reg(t, op.Reg)
	if r.addr == "" {
		return infeasible(op, "register %q holds no pointer", op.Reg)
	}
	if m.cfg.RequireConsistentReread && !r.verified {
		return m.fail(ErrDerefBeforeConsistency, op, r.addr, r.identity,
			"deref of %q before a consistent root re-read", r.addr)
	}

	// Even after verification, deref is only legal while protection still
	// covers the object and the object has not been reclaimed.
	if m.cfg.RequireConsistentReread {
		covered := false
		for _, view := range t.visibleHP {
			if !view.valid || view.addr != r.addr {
				continue
			}
			if m.cfg.TrackIdentity && view.identity != r.identity {
				continue
			}
			covered = true
			break
		}
		if !covered {
			if m.cfg.TrackIdentity && m.protectsDifferentGeneration(t, r.addr) {
				return m.fail(ErrAddressReuse, op, r.addr, r.identity,
					"deref through a hazard slot that now names a reused generation")
			}
			return m.fail(ErrUseAfterFree, op, r.addr, r.identity,
				"deref after protection for %q was withdrawn", r.addr)
		}
	}

	o := m.liveObject(r.addr)
	if o == nil {
		return m.fail(ErrUseAfterFree, op, r.addr, r.identity,
			"object at %q was reclaimed before deref", r.addr)
	}
	if r.identity != 0 && o.identity != r.identity {
		return m.fail(ErrAddressReuse, op, r.addr, r.identity,
			"address %q was reused: deref expected generation %d, found %d",
			r.addr, r.identity, o.identity)
	}
	return Violation{}
}

func (m *Machine) stepRetire(t *thread, op Op) Violation {
	if op.Addr == "" {
		return infeasible(op, "Retire requires Addr")
	}
	o := m.liveObject(op.Addr)
	if o == nil {
		return infeasible(op, "Retire of non-live address %q", op.Addr)
	}
	if _, exists := m.retired[op.Addr]; exists {
		return infeasible(op, "address %q already retired", op.Addr)
	}
	m.retired[op.Addr] = retiredEntry{
		addr:      op.Addr,
		identity:  o.identity,
		retiredAt: m.step,
	}
	return Violation{}
}
