package loopkit

// memLoc is a symbolic abstract memory location.
type memLoc struct {
	base    *Instr // alloca; nil means unknown/escaped base
	offset  int64  // GEP element offset relative to base
	unknown bool   // conservative "may be anything" bucket
}

// resolveAddr traces an address through GEP chains to its alloca base.
// Any non-GEP/non-alloca pointer yields an unknown conservative location.
func resolveLoc(addr *Instr) memLoc {
	off := int64(0)
	for addr != nil {
		switch addr.Op {
		case OpGEP:
			off += addr.GepIdx
			addr = addr.Operands[0]
		case OpAlloca:
			return memLoc{base: addr, offset: off}
		default:
			return memLoc{unknown: true}
		}
	}
	return memLoc{unknown: true}
}

func mayAlias(a, b memLoc) bool {
	if a.unknown || b.unknown {
		return true
	}
	return a.base == b.base && a.offset == b.offset
}

// MemEffects summarizes memory operations observed over a region.
type MemEffects struct {
	stores []memLoc
	calls  []*Instr // side-effecting calls
}

func collectEffects(blocks map[*Block]bool) MemEffects {
	var eff MemEffects
	for b := range blocks {
		for _, i := range b.Instrs {
			switch i.Op {
			case OpStore:
				eff.stores = append(eff.stores, resolveLoc(i.Operands[0]))
			case OpCall:
				if !callIsPure(i) {
					eff.calls = append(eff.calls, i)
				}
			}
		}
	}
	return eff
}

// mayAliasStore reports whether any recorded store may hit loc.
func (m MemEffects) mayAliasStore(loc memLoc) bool {
	for _, s := range m.stores {
		if mayAlias(s, loc) {
			return true
		}
	}
	return false
}

// addrIsLoopInvariant reports whether every instruction used to build
// addr (GEP chain) is loop invariant.
func addrIsLoopInvariant(addr *Instr, inLoop map[*Block]bool, invariant map[*Instr]bool) bool {
	for addr != nil {
		switch addr.Op {
		case OpGEP:
			if inLoop[addr.Block] && !invariant[addr] {
				return false
			}
			addr = addr.Operands[0]
		case OpAlloca:
			// Allocas live in the entry block in this IR; a body alloca
			// is still invariant per address identity.
			return true
		default:
			return !inLoop[addr.Block] || invariant[addr]
		}
	}
	return false
}
