package volrecover

import "sort"

// HeaderInfo is the verifier result for one stored header.
type HeaderInfo struct {
	Header *Header
	// Reason is zero when the header is fully authenticated and every slot
	// binding is satisfied by the image.
	Reason Reason
	// Reachable means the header's parent chain terminates at genesis using
	// headers stored in this image.
	Reachable bool
	// Usable means the header is valid, reachable, and grants no revoked slot.
	Usable bool
}

// Verdict is the result of analyzing a read-only image under a trust root.
type Verdict struct {
	Headers     []*HeaderInfo
	Valid       []Digest
	Usable      []Digest
	RootHeader  Digest
	RootUsable  bool
	Fork        bool
	MixedGen    bool
	FatalReason Reason
}

// SafeToUnlock reports whether the image contains at least one complete,
// trusted authorization with no unresolved fork or mixed-generation failure.
func (v *Verdict) SafeToUnlock() bool {
	return v.FatalReason.OK() && len(v.Usable) > 0
}

// Analyze verifies every header against the trust root and the image and
// applies the generation, fork, and revocation rules.
func Analyze(view ImageView, root *TrustRoot) *Verdict {
	verdict := &Verdict{
		Headers:    []*HeaderInfo{},
		Valid:      []Digest{},
		Usable:     []Digest{},
		RootHeader: view.RootPointer(),
	}

	headers := view.Headers()
	info := make(map[Digest]*HeaderInfo, len(headers))

	for _, h := range headers {
		hi := &HeaderInfo{Header: h}
		hi.Reason = validateHeader(h, view, root)
		info[h.ID] = hi
		verdict.Headers = append(verdict.Headers, hi)
		if hi.Reason.OK() {
			verdict.Valid = append(verdict.Valid, h.ID)
		}
	}

	// Ancestry is evaluated only among authenticated, fully valid headers.
	// A chain must reach the zero-digest genesis without gaps.
	for id, hi := range info {
		if !hi.Reason.OK() {
			continue
		}
		seen := map[Digest]bool{}
		cur := id
		for {
			h, ok := info[cur]
			if !ok || !h.Reason.OK() {
				hi.Reason = Reason{Code: ReasonBrokenAncestry, Detail: "parent header missing or invalid", Witness: shortID(cur)}
				break
			}
			if seen[cur] {
				hi.Reason = Reason{Code: ReasonBrokenAncestry, Detail: "ancestry cycle", Witness: shortID(cur)}
				break
			}
			seen[cur] = true
			parent := h.Header.Parent
			if parent == (Digest{}) {
				hi.Reachable = true
				break
			}
			cur = parent
		}
	}

	valid := make([]*HeaderInfo, 0, len(verdict.Valid))
	for _, id := range verdict.Valid {
		hi := info[id]
		if hi.Reachable {
			valid = append(valid, hi)
		}
	}

	// A coherent upgrade has at most one reachable valid header per epoch,
	// ordered into a single linear chain. Two reachable valid headers sharing
	// an epoch are distinct bodies and therefore a fork.
	byEpoch := map[Epoch][]Digest{}
	for _, hi := range valid {
		byEpoch[hi.Header.Epoch] = append(byEpoch[hi.Header.Epoch], hi.Header.ID)
	}
	epochs := make([]Epoch, 0, len(byEpoch))
	for epoch := range byEpoch {
		epochs = append(epochs, epoch)
	}
	sortEpochs(epochs)
	for _, epoch := range epochs {
		ids := byEpoch[epoch]
		if len(ids) > 1 {
			verdict.Fork = true
			verdict.FatalReason = Reason{
				Code:    ReasonFork,
				Detail:  "two distinct valid headers claim the same generation",
				Witness: "epoch=" + u64Name(uint64(epoch)),
			}
			break
		}
	}

	// Mixed generation: reachable valid headers must form one linear chain.
	if verdict.FatalReason.OK() && len(valid) > 1 {
		chain := map[Digest]bool{}
		for _, hi := range valid {
			chain[hi.Header.ID] = true
		}
		link := map[Digest]Digest{}
		for _, hi := range valid {
			link[hi.Header.ID] = hi.Header.Parent
		}
		roots := 0
		for id := range chain {
			parent := link[id]
			if parent == (Digest{}) {
				roots++
				continue
			}
			if !chain[parent] {
				roots++
			}
		}
		if roots != 1 {
			verdict.MixedGen = true
			verdict.FatalReason = Reason{
				Code:   ReasonMixedGeneration,
				Detail: "valid headers do not form a single parent-linked generation chain",
			}
		}
	}

	// The monotonic revocation register outranks old headers: any header that
	// still grants a revoked slot is not usable and cannot restore the slot.
	imageRevocations := view.Revocations()
	for _, hi := range valid {
		revoked := false
		for _, grant := range hi.Header.Grants {
			if _, revokedInImage := imageRevocations[grant.Slot]; revokedInImage || root.IsRevoked(grant.Slot) {
				revoked = true
				hi.Reason = Reason{
					Code:    ReasonRevokedSlot,
					Detail:  "header grants a slot present in the revocation register",
					Witness: "slot=" + u64Name(uint64(grant.Slot)),
				}
				break
			}
		}
		if !revoked {
			hi.Usable = true
			verdict.Usable = append(verdict.Usable, hi.Header.ID)
		}
	}

	// Rebuild the valid id list after reachability filtering.
	verdict.Valid = verdict.Valid[:0]
	for _, hi := range valid {
		verdict.Valid = append(verdict.Valid, hi.Header.ID)
	}

	if rootHdr, ok := view.Header(view.RootPointer()); ok {
		if hi, found := info[rootHdr.ID]; found {
			verdict.RootUsable = found && hi.Reachable && hi.Usable
		}
	}

	return verdict
}

func validateHeader(h *Header, view ImageView, root *TrustRoot) Reason {
	if reason := VerifyHeader(h, root); !reason.OK() {
		return reason
	}
	seenSlots := map[SlotID]bool{}
	seenUnits := map[UnitID]bool{}
	for _, slot := range h.Slots {
		if !slot.Range.Valid() {
			return Reason{Code: ReasonRangeConflict, Detail: "invalid slot interval", Witness: "slot=" + u64Name(uint64(slot.ID))}
		}
		if seenSlots[slot.ID] {
			return Reason{Code: ReasonMalformedHeader, Detail: "duplicate slot id", Witness: "slot=" + u64Name(uint64(slot.ID))}
		}
		seenSlots[slot.ID] = true
		for _, ref := range slot.Units {
			if !slot.Range.Contains(uint64(ref.Unit)) {
				return Reason{Code: ReasonRangeConflict, Detail: "unit outside declared slot range", Witness: "unit=" + u64Name(uint64(ref.Unit))}
			}
			if seenUnits[ref.Unit] {
				return Reason{Code: ReasonRangeConflict, Detail: "unit bound by more than one slot", Witness: "unit=" + u64Name(uint64(ref.Unit))}
			}
			seenUnits[ref.Unit] = true
			rec, ok := view.Unit(ref.Unit)
			if !ok {
				return Reason{Code: ReasonDanglingUnit, Witness: "unit=" + u64Name(uint64(ref.Unit))}
			}
			// Exact version binding: a newer durable unit must never be
			// interpreted as the older content an old slot expects.
			if rec.Version > ref.Version {
				return Reason{Code: ReasonVersionRollback, Witness: "unit=" + u64Name(uint64(ref.Unit))}
			}
			if rec.Version != ref.Version || rec.Commit != ref.Commit {
				return Reason{Code: ReasonCommitMismatch, Witness: "unit=" + u64Name(uint64(ref.Unit))}
			}
		}
	}

	for _, seg := range h.Segments {
		if !seg.Valid() {
			return Reason{Code: ReasonRangeConflict, Detail: "invalid segment interval"}
		}
	}

	for _, grant := range h.Grants {
		if !seenSlots[grant.Slot] {
			return Reason{Code: ReasonMalformedHeader, Detail: "grant references unknown slot", Witness: "slot=" + u64Name(uint64(grant.Slot))}
		}
		if int(grant.Segment) >= len(h.Segments) {
			return Reason{Code: ReasonMalformedHeader, Detail: "grant references unknown segment", Witness: "segment=" + u64Name(uint64(grant.Segment))}
		}
	}
	return Reason{}
}

func sortEpochs(epochs []Epoch) {
	sort.Slice(epochs, func(i, j int) bool { return epochs[i] < epochs[j] })
}

func u64Name(v uint64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	for v > 0 {
		n--
		buf[n] = digits[v%10]
		v /= 10
	}
	return string(buf[n:])
}
