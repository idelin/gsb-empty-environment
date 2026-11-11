package pgoutput

import "fmt"

// appendChange routes a change into the active stream segment or the open
// non-streamed transaction and maintains the streamed row index.
func (a *Assembler) appendChange(ch Change) error {
	if a.activeXID != 0 {
		ch.SubXID = a.activeSubXID
		buf := a.streams[a.activeXID]
		idx := len(buf.changes)
		buf.changes = append(buf.changes, ch)
		buf.subChanges[ch.SubXID] = append(buf.subChanges[ch.SubXID], idx)
		return nil
	}
	if a.current == nil {
		return fmt.Errorf("pgoutput: %s change for relation %d outside of a transaction",
			ch.Op, ch.Relation.ID)
	}
	a.current.changes = append(a.current.changes, ch)
	return nil
}

func (a *Assembler) handleStreamStart(s StreamStart) error {
	if a.activeXID != 0 {
		return fmt.Errorf("pgoutput: Stream Start for XID %d while XID %d segment is open",
			s.XID, a.activeXID)
	}
	if a.current != nil {
		return fmt.Errorf("pgoutput: Stream Start for XID %d interleaves open transaction XID %d",
			s.XID, a.current.xid)
	}
	if s.FirstSegment {
		if _, exists := a.streams[s.XID]; exists {
			return fmt.Errorf("pgoutput: first stream segment of XID %d but a buffer already exists", s.XID)
		}
		a.streams[s.XID] = newStreamBuf(s.XID)
	} else if _, exists := a.streams[s.XID]; !exists {
		return fmt.Errorf("pgoutput: continuation stream segment of XID %d without a prior segment", s.XID)
	}
	a.activeXID = s.XID
	a.activeSubXID = s.SubXID
	return nil
}

func (a *Assembler) handleStreamStop() error {
	if a.activeXID == 0 {
		return errNoOpenStream
	}
	a.activeXID = 0
	a.activeSubXID = 0
	return nil
}

func (a *Assembler) handleStreamCommit(c StreamCommit) error {
	if a.activeXID != 0 {
		return fmt.Errorf("pgoutput: Stream Commit for XID %d before Stream Stop", c.XID)
	}
	buf, ok := a.streams[c.XID]
	if !ok {
		return fmt.Errorf("pgoutput: Stream Commit for unknown XID %d", c.XID)
	}
	txn := &Transaction{
		XID:        c.XID,
		CommitLSN:  c.CommitLSN,
		EndLSN:     c.EndLSN,
		CommitTime: c.CommitTime,
		Changes:    buf.changes,
		Messages:   make([]LogicalMessage, 0, len(buf.messages)),
	}
	for _, sm := range buf.messages {
		txn.Messages = append(txn.Messages, sm.msg)
	}
	delete(a.streams, c.XID)
	a.ready = append(a.ready, txn)
	return nil
}

// handleStreamAbort implements the streaming abort rules.
//
// On protocol v2 the server cannot isolate a subtransaction inside an already
// streamed segment, so a subtransaction abort forces the whole top-level
// transaction to be invalidated and re-streamed from scratch: the entire
// buffer is dropped and the next Stream Start must carry FirstSegment again.
// When the decoder can attribute changes to subtransactions (v4 subxid stream
// framing), only the aborted subtransaction's changes are removed while the
// rest of the streamed transaction survives.
func (a *Assembler) handleStreamAbort(s StreamAbort) error {
	buf, ok := a.streams[s.XID]
	if !ok {
		return fmt.Errorf("pgoutput: Stream Abort for unknown XID %d", s.XID)
	}

	// Aborting the top-level transaction: discard everything.
	if !s.SubAbort || s.SubXID == s.XID || s.SubXID == 0 {
		delete(a.streams, s.XID)
		if a.activeXID == s.XID {
			a.activeXID = 0
			a.activeSubXID = 0
		}
		return nil
	}

	// Subtransaction abort with subxid-annotated changes: drop just those.
	indices, ok := buf.subChanges[s.SubXID]
	if !ok {
		// Nothing from that subtransaction was streamed; nothing to remove.
		return nil
	}
	drop := make(map[int]struct{}, len(indices))
	for _, idx := range indices {
		drop[idx] = struct{}{}
	}

	kept := make([]Change, 0, len(buf.changes)-len(drop))
	for i, ch := range buf.changes {
		if _, removed := drop[i]; removed {
			continue
		}
		kept = append(kept, ch)
	}
	buf.changes = kept
	delete(buf.subChanges, s.SubXID)

	keptMsgs := make([]streamMessage, 0, len(buf.messages))
	for _, sm := range buf.messages {
		if sm.subXID != s.SubXID {
			keptMsgs = append(keptMsgs, sm)
		}
	}
	buf.messages = keptMsgs

	a.rebuildSubChangeIndex(buf)
	return nil
}

// rebuildSubChangeIndex reconstructs the subXID -> change-index map after a
// subtransaction rollback invalidated the stored positions.
func (a *Assembler) rebuildSubChangeIndex(buf *streamBuf) {
	buf.subChanges = make(map[uint32][]int)
	for idx, ch := range buf.changes {
		buf.subChanges[ch.SubXID] = append(buf.subChanges[ch.SubXID], idx)
	}
}
