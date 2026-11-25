package ptrauth

import "math/bits"

// addOverflow returns a+b and reports whether the addition wraps 64 bits.
func addOverflow(a, b uint64) (uint64, bool) {
	sum, carry := bits.Add64(a, b, 0)
	return sum, carry != 0
}

// Derive recomputes authentication for an offset pointer or sub-slice. The
// derived pointer may only preserve or narrow the parent kind and stays in
// the parent context; use Recontext for an authenticated context transfer.
//
// The new address, span, kind, generation and context are all re-bound into
// a fresh HMAC, so an attacker cannot relabel a derived data pointer as
// executable or extend its span without the authenticator key.
func (r *Registry) Derive(parent TaggedPtr, ctx ContextID, offset, size uint64, kind Kind) (TaggedPtr, error) {
	if err := r.verifyTag(parent, ctx); err != nil {
		return TaggedPtr{}, err
	}
	child, err := kind.normalize()
	if err != nil {
		return TaggedPtr{}, err
	}
	if !IsNarrowing(parent.kind, child) {
		return TaggedPtr{}, ErrKindNotPermitted
	}
	if err := spanCheck(parent, offset, size); err != nil {
		return TaggedPtr{}, err
	}
	newAddress, overflow := addOverflow(parent.address, offset)
	if overflow {
		return TaggedPtr{}, ErrOffsetOverflow
	}

	obj, unlock, err := r.lookup(parent, false)
	if err != nil {
		return TaggedPtr{}, err
	}
	defer unlock()
	if err := checkKindForObject(obj, child); err != nil {
		return TaggedPtr{}, err
	}
	return r.mint(newAddress, obj.generation.Load(), child, parent.context, size)
}

// Subslice derives a pointer covering [offset, offset+size) with the parent's
// exact kind and context, re-authenticating the new address and span.
func (r *Registry) Subslice(parent TaggedPtr, ctx ContextID, offset, size uint64) (TaggedPtr, error) {
	return r.Derive(parent, ctx, offset, size, parent.kind)
}

// Recontext re-authenticates an existing pointer for targetContext. It is
// the only sanctioned way to move a capability across call contexts: the
// source context is verified first, then a fresh HMAC binds the new
// context. Raw field swapping fails verifyTag with ErrTagForged or
// ErrContextMismatch, and the resulting pointer is never downgraded through
// the gate.
//
// kind may preserve or narrow ptr's rights; widening is rejected.
func (r *Registry) Recontext(ptr TaggedPtr, source, target ContextID, kind Kind) (TaggedPtr, error) {
	if err := r.verifyTag(ptr, source); err != nil {
		return TaggedPtr{}, err
	}
	if target == "" {
		return TaggedPtr{}, ErrContextMismatch
	}
	child, err := kind.normalize()
	if err != nil {
		return TaggedPtr{}, err
	}
	if !IsNarrowing(ptr.kind, child) {
		return TaggedPtr{}, ErrKindNotPermitted
	}

	obj, unlock, err := r.lookup(ptr, false)
	if err != nil {
		return TaggedPtr{}, err
	}
	defer unlock()
	if err := checkKindForObject(obj, child); err != nil {
		return TaggedPtr{}, err
	}
	return r.mint(ptr.address, obj.generation.Load(), child, target, ptr.size)
}
