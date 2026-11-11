package netsec

import "sort"

// ReferenceSnapshot replays a complete journal from scratch and returns
// the recovered block contents. It is the deliberately simple full-replay
// reference: assemble every transaction, keep the ones that are fully
// committed and pass validation, order them in modular sequence space,
// and apply their operations to a flat block map. Recoverer.Snapshot
// must agree with ReferenceSnapshot on every log.
func ReferenceSnapshot(entries []Entry) (map[BlockID][]byte, error) {
	var committed []*transaction
	asm := newAssembler(func(t *transaction) {
		committed = append(committed, t)
	})
	for _, e := range entries {
		if err := asm.ingest(e); err != nil {
			return nil, err
		}
	}

	origin, _ := windowSpan(asm.seqs)
	sort.Slice(committed, func(i, j int) bool {
		ki, kj := seqKey(committed[i].seq, origin), seqKey(committed[j].seq, origin)
		if ki != kj {
			return ki < kj
		}
		return committed[i].id < committed[j].id
	})

	snap := make(map[BlockID][]byte)
	for _, t := range committed {
		for _, op := range t.sortedOps() {
			if op.kind == KindUndo {
				delete(snap, op.block)
			} else {
				snap[op.block] = cloneBytes(op.data)
			}
		}
	}
	return snap, nil
}
