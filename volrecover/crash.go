package volrecover

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// CrashState is one distinct reachable post-crash image.
type CrashState struct {
	// Epoch is the flush epoch after which the crash occurred.
	Epoch int
	// SurvivedWrites counts write events present in this state.
	SurvivedWrites int
	View           *MemView
}

// CrashWitness pairs an unsafe crash image with the verifier verdict.
type CrashWitness struct {
	State   *CrashState
	Verdict *Verdict
}

// ExhaustCrashes enumerates every reachable persistence subset. Within one
// flush epoch any subset of writes may survive; across flushes persistence is
// prefix-ordered, so all writes before an earlier barrier remain durable once
// later writes persist. Revocation writes are treated as ordinary write
// events for crash purposes, but the register itself only moves upward.
func ExhaustCrashes(initial ImageView, actions []Action, limit int) ([]*CrashState, *Reason) {
	if limit <= 0 {
		limit = DefaultEnumerationLimit
	}
	base, ok := initial.(*MemView)
	if !ok {
		base = memViewFromInterface(initial)
	}

	epochs := splitEpochs(actions)
	states := []*CrashState{}
	seen := map[string]bool{}

	add := func(epoch, survived int, store memStore) bool {
		view := &MemView{store: store}
		fp := fingerprint(view)
		if seen[fp] {
			return true
		}
		seen[fp] = true
		if len(states) >= limit {
			return false
		}
		states = append(states, &CrashState{Epoch: epoch, SurvivedWrites: survived, View: view})
		return true
	}

	// Crashes before any action.
	if !add(0, 0, cloneStore(base.store)) {
		return nil, &Reason{Code: ReasonEnumerationLimit, Detail: "crash-state budget exhausted while materializing states"}
	}

	prefix := cloneStore(base.store)
	prefixWrites := 0
	for epochIndex, writes := range epochs {
		n := len(writes)
		for mask := 0; mask < (1 << uint(n)); mask++ {
			candidate := cloneStore(prefix)
			survived := prefixWrites
			for bit := 0; bit < n; bit++ {
				if mask&(1<<uint(bit)) == 0 {
					continue
				}
				applyWrite(&candidate, writes[bit])
				survived++
			}
			if !add(epochIndex+1, survived, candidate) {
				return nil, &Reason{Code: ReasonEnumerationLimit, Detail: "crash-state budget exhausted while materializing states", Witness: "states=" + u64Name(uint64(len(states)))}
			}
		}
		// The flush succeeds only when every write in this epoch is durable:
		// this becomes the mandatory prefix for the next epoch.
		for _, write := range writes {
			applyWrite(&prefix, write)
			prefixWrites++
		}
	}
	return states, nil
}

func splitEpochs(actions []Action) [][]Action {
	epochs := [][]Action{}
	current := []Action{}
	for _, action := range actions {
		if action.Kind == Flush {
			epochs = append(epochs, current)
			current = []Action{}
			continue
		}
		current = append(current, action)
	}
	if len(current) > 0 {
		epochs = append(epochs, current)
	}
	return epochs
}

func applyWrite(store *memStore, action Action) {
	switch action.Kind {
	case WriteUnit:
		rec := action.Record
		hasRecord := action.Record.Commit != (Digest{}) || action.Record.Version != 0 || action.Record.Unit != 0
		if !hasRecord && action.Payload != nil {
			rec = Record{Unit: action.Unit, Version: action.Version, Commit: UnitCommit(action.Payload)}
		}
		if !hasRecord {
			rec.Unit = action.Unit
		}
		store.records[rec.Unit] = rec
	case WriteHeader:
		if action.Header != nil {
			cp := *action.Header
			if len(action.Header.Sig) != 0 {
				cp.Sig = append([]byte(nil), action.Header.Sig...)
			}
			store.headers[cp.ID] = &cp
		}
	case WriteRoot:
		store.root = action.Root
	case WriteRevocation:
		// Monotonic semantics: a later surviving sequence cannot be undone by
		// an earlier subset; max() is therefore safe under subset enumeration.
		if old, ok := store.revocation[action.Slot]; !ok || action.RevokeSeq > old {
			store.revocation[action.Slot] = action.RevokeSeq
		}
	case Flush:
		// Barriers delimit epochs in splitEpochs and carry no state mutation.
	}
}

func firstUnsafe(states []*CrashState, root *TrustRoot) *CrashWitness {
	for _, state := range states {
		verdict := Analyze(state.View, root)
		if !verdict.SafeToUnlock() {
			return &CrashWitness{State: state, Verdict: verdict}
		}
	}
	return nil
}

func reasonFromWitness(witness *CrashWitness) Reason {
	if !witness.Verdict.FatalReason.OK() {
		return witness.Verdict.FatalReason
	}
	return Reason{Code: ReasonNoTrustedHeader, Detail: "crash image contains no complete trusted authorization", Witness: "epoch=" + u64Name(uint64(witness.State.Epoch))}
}

func cloneMemView(view *MemView) *MemView {
	return &MemView{store: cloneStore(view.store)}
}

func memViewFromInterface(view ImageView) *MemView {
	store := memStore{
		records:    map[UnitID]Record{},
		headers:    map[Digest]*Header{},
		root:       view.RootPointer(),
		revocation: view.Revocations(),
	}
	for _, rec := range view.Units() {
		store.records[rec.Unit] = rec
	}
	for _, h := range view.Headers() {
		cp := *h
		store.headers[h.ID] = &cp
	}
	return &MemView{store: store}
}

// fingerprint deterministically captures all safety-relevant state so that
// crash subsets converging on the same image are enumerated once.
func fingerprint(view *MemView) string {
	h := sha256.New()
	h.Write([]byte("volrecover-fp/v1\n"))
	var buf [8]byte

	h.Write(view.store.root[:])

	unitIDs := make([]UnitID, 0, len(view.store.records))
	for id := range view.store.records {
		unitIDs = append(unitIDs, id)
	}
	sort.Slice(unitIDs, func(i, j int) bool { return unitIDs[i] < unitIDs[j] })
	for _, id := range unitIDs {
		rec := view.store.records[id]
		binary.BigEndian.PutUint64(buf[:], uint64(rec.Unit))
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], rec.Version)
		h.Write(buf[:])
		h.Write(rec.Commit[:])
	}

	headerIDs := make([]Digest, 0, len(view.store.headers))
	for id := range view.store.headers {
		headerIDs = append(headerIDs, id)
	}
	sort.Slice(headerIDs, func(i, j int) bool {
		for b := 0; b < 32; b++ {
			if headerIDs[i][b] != headerIDs[j][b] {
				return headerIDs[i][b] < headerIDs[j][b]
			}
		}
		return false
	})
	for _, id := range headerIDs {
		h.Write(id[:])
	}

	slots := make([]SlotID, 0, len(view.store.revocation))
	for slot := range view.store.revocation {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, slot := range slots {
		binary.BigEndian.PutUint32(buf[:4], uint32(slot))
		h.Write(buf[:4])
		binary.BigEndian.PutUint64(buf[:], view.store.revocation[slot])
		h.Write(buf[:])
	}

	out := h.Sum(nil)
	return string(out)
}
