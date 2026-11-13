package netsec

import "fmt"

// ReviewHost is the implementation pending security review. DO NOT SHIP.
//
// It trusts the request in ways Host does not:
//  1. Process references are resolved by ID only; the process generation is
//     never compared, so requests bound to a pre-restart generation still
//     resolve after the target process restarts.
//  2. Handle lookup checks only that the slot is open; the handle generation
//     and the object generation are ignored, so a stale request lands on
//     whatever object the reused slot now points to.
//  3. The broker "check" compares only process IDs inside the capability; it
//     never verifies the host MAC, the bound generations, or the object type,
//     so a forged or stale capability passes.
//  4. sameAccess combined with a non-zero desiredAccess is not rejected; the
//     two masks are silently unioned.
//  5. desiredAccess is trusted verbatim: generic bits are never expanded and
//     the result is never checked against the source grantedAccess or the
//     object copyable mask.
//  6. DuplicateBatch publishes each item as it goes, leaking partial results
//     when a later item fails.
type ReviewHost struct {
	*Host
}

// NewReviewHost creates a ReviewHost with the given capability MAC secret.
func NewReviewHost(secret uint64) *ReviewHost {
	return &ReviewHost{Host: NewHost(secret)}
}

// Duplicate implements the trusting validation described on ReviewHost.
func (r *ReviewHost) Duplicate(req DuplicateRequest) (HandleRef, error) {
	h := r.Host
	src, ok := h.procs[req.Source.ID] // flaw 1: generation ignored
	if !ok {
		return HandleRef{}, ErrNoSuchProcess
	}
	tgt, ok := h.procs[req.Target.ID] // flaw 1: generation ignored
	if !ok {
		return HandleRef{}, ErrNoSuchProcess
	}
	if int(req.SourceHandle.Slot) >= len(src.table) {
		return HandleRef{}, ErrNoSuchHandle
	}
	slot := &src.table[req.SourceHandle.Slot]
	if !slot.open { // flaw 2: handle and object generations ignored
		return HandleRef{}, ErrStaleHandle
	}
	obj := h.objects[slot.object]
	if src.integrity != tgt.integrity {
		c := req.Broker
		if c == nil || c.Source.ID != src.id || c.Target.ID != tgt.id {
			// flaw 3: MAC, generation binding and object type never checked
			return HandleRef{}, ErrBrokerRequired
		}
	}
	access := slot.granted
	if req.DesiredAccess != 0 {
		// flaws 4+5: no conflict rejection, no generic expansion, no
		// subset check against grantedAccess or the copyable mask
		access |= req.DesiredAccess
	}
	return h.publish(dupPlan{tgt: tgt, obj: obj, access: access, inherit: req.Inherit}), nil
}

// DuplicateBatch publishes item by item; a failure leaks the handles that
// were already installed (flaw 6).
func (r *ReviewHost) DuplicateBatch(reqs []DuplicateRequest) ([]HandleRef, error) {
	refs := make([]HandleRef, 0, len(reqs))
	for i, req := range reqs {
		ref, err := r.Duplicate(req)
		if err != nil {
			return refs, fmt.Errorf("batch item %d: %w", i, err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// CloneSystem deep-copies the review host for model checking.
func (r *ReviewHost) CloneSystem() System {
	return &ReviewHost{Host: r.Host.clone()}
}
