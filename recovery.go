package netsec

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Recover replays a complete audit journal into a fresh Manager. The signed
// checkpoint is a high-water mark kept outside the journal: recovery rejects a
// truncated/rolled-back journal or a forked record at the checkpoint index.
//
// The manager must be freshly constructed with the same master secret,
// retention policy, and recovery key as the crashed process. Recovery rebuilds
// live CIDs, tokens, key references, and CID tombstones before any new CID can
// be issued.
func (m *Manager) Recover(j Journal, cp Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mutated || len(m.audit) > 0 || len(m.records) > 0 || m.seq != 0 {
		return ErrAlreadyRecovered
	}
	if j == nil {
		return fmt.Errorf("%w: nil journal", ErrJournalCorrupt)
	}
	if err := m.VerifyCheckpoint(cp); err != nil {
		return err
	}
	records, err := j.Records()
	if err != nil {
		return err
	}
	if err := m.VerifyAuditRecords(records); err != nil {
		return err
	}
	var zero [32]byte
	zeroHex := hex.EncodeToString(zero[:])
	if cp.Sequence > uint64(len(records)) {
		return fmt.Errorf("%w: checkpoint sequence %d, log has %d", ErrRollbackDetected, cp.Sequence, len(records))
	}
	if cp.Sequence == 0 {
		if cp.Digest != zeroHex {
			return ErrRollbackDetected
		}
	} else {
		at := records[cp.Sequence-1]
		if at.Hash != cp.Digest || at.Seq != cp.Sequence {
			return fmt.Errorf("%w: checkpoint digest does not match record %d", ErrRollbackDetected, cp.Sequence)
		}
	}

	for _, w := range records {
		if w.Seq <= m.seq {
			return fmt.Errorf("%w: duplicate replay sequence %d", ErrJournalRollback, w.Seq)
		}
		if err := m.applyReplayLocked(w); err != nil {
			return err
		}
		var hash [32]byte
		decoded, err := hex.DecodeString(w.Hash)
		if err != nil || len(decoded) != len(hash) {
			return ErrJournalCorrupt
		}
		copy(hash[:], decoded)
		m.seq = w.Seq
		m.lastHash = hash
		m.audit = append(m.audit, w)
	}
	m.mutated = true
	// Expire anything whose deadline elapsed while the process was down. The
	// resulting witnesses append to the same journal through commitLocked.
	if err := m.purgeExpiredLocked(); err != nil {
		return err
	}
	// A crash can occur between the connection.closed record and the final
	// cid.closed records. Complete the deterministic close during replay.
	if m.closed {
		if _, err := m.closeLocked(m.closeReason); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) applyReplayLocked(w AuditWitness) error {
	if w.Connection != "" {
		if m.label == "" {
			m.label = w.Connection
		} else if m.label != w.Connection {
			return fmt.Errorf("%w: witness label %q, manager label %q", ErrJournalCorrupt, w.Connection, m.label)
		}
	}
	switch w.Kind {
	case AuditCIDIssued:
		return m.replayIssueLocked(w)
	case AuditCIDConfirmed:
		rec, err := m.replayRecordLocked(w)
		if err != nil {
			return err
		}
		if rec.state != w.From || w.To != CIDActive || !allowedTransition(rec.state, w.To) {
			return fmt.Errorf("%w: invalid peer-confirm transition", ErrJournalCorrupt)
		}
		rec.state = CIDActive
		return nil
	case AuditCIDRetireRequested:
		rec, err := m.replayRecordLocked(w)
		if err != nil {
			return err
		}
		if rec.state != w.From || w.To != CIDRetiring || !allowedTransition(rec.state, w.To) {
			return fmt.Errorf("%w: invalid retire-request transition", ErrJournalCorrupt)
		}
		rec.state = CIDRetiring
		rec.retireRequestedAt = w.At.UTC()
		if rec.deadline.Before(m.retentionDeadline(rec.retireRequestedAt)) {
			rec.deadline = m.retentionDeadline(rec.retireRequestedAt)
		}
		return nil
	case AuditCIDPeerRetired:
		rec, err := m.replayRecordLocked(w)
		if err != nil {
			return err
		}
		if rec.state != w.From || w.To != CIDRetired || !allowedTransition(rec.state, w.To) {
			return fmt.Errorf("%w: invalid peer-retire transition", ErrJournalCorrupt)
		}
		rec.state = CIDRetired
		rec.peerRetiredAt = w.At.UTC()
		if d := m.retentionDeadline(rec.peerRetiredAt); rec.deadline.Before(d) {
			rec.deadline = d
		}
		return nil
	case AuditCIDClosed:
		rec, err := m.replayRecordLocked(w)
		if err != nil {
			return err
		}
		if rec.state != w.From || w.To != CIDClosed || !allowedTransition(rec.state, w.To) {
			return fmt.Errorf("%w: invalid close transition", ErrJournalCorrupt)
		}
		rec.state = CIDClosed
		rec.closedAt = w.At.UTC()
		if d := m.retentionDeadline(rec.closedAt); rec.deadline.Before(d) {
			rec.deadline = d
		}
		return nil
	case AuditCIDExpired:
		rec, err := m.replayRecordLocked(w)
		if err != nil {
			return err
		}
		if rec.state != w.From || w.To != CIDExpired || !allowedTransition(rec.state, w.To) {
			return fmt.Errorf("%w: invalid expiry transition", ErrJournalCorrupt)
		}
		generation := rec.generation
		rec.state = CIDExpired
		rec.deadline = time.Time{}
		m.removeTokenLocked(rec)
		if err := m.keys.Release(generation); err != nil {
			return fmt.Errorf("%w: key reference release: %v", ErrJournalCorrupt, err)
		}
		return nil
	case AuditConnectionClosed:
		m.closed = true
		m.closeReason = w.Reason
		return nil
	case AuditPathRebound:
		if idx := strings.LastIndex(w.Reason, "->"); idx >= 0 {
			m.lastPath = w.Reason[idx+2:]
		}
		return nil
	case AuditKeyRotated:
		if err := m.keys.setCurrentForRecovery(w.Generation); err != nil {
			return fmt.Errorf("%w: %v", ErrJournalCorrupt, err)
		}
		return nil
	case AuditKeyDestroyed:
		if err := m.keys.Destroy(w.Generation); err != nil {
			return fmt.Errorf("%w: replay key destroy: %v", ErrJournalCorrupt, err)
		}
		return nil
	case AuditCIDReuseRejected, AuditTokenCollision, AuditResetAccepted, AuditResetAmbiguous:
		// These records document rejected inputs or the trigger for a close;
		// they do not mutate CID state themselves.
		return nil
	default:
		return fmt.Errorf("%w: unknown audit kind %q", ErrJournalCorrupt, w.Kind)
	}
}

func (m *Manager) replayRecordLocked(w AuditWitness) (*cidRecord, error) {
	cid, err := hex.DecodeString(w.CIDHex)
	if err != nil {
		return nil, fmt.Errorf("%w: bad CID encoding", ErrJournalCorrupt)
	}
	rec, ok := m.records[string(cid)]
	if !ok {
		return nil, fmt.Errorf("%w: transition for unknown CID %x", ErrJournalCorrupt, cid)
	}
	if rec.sequence != w.CIDSeq || rec.generation != w.Generation {
		return nil, fmt.Errorf("%w: CID metadata mismatch", ErrJournalCorrupt)
	}
	return rec, nil
}

func (m *Manager) replayIssueLocked(w AuditWitness) error {
	if w.From != CIDNone || w.To != CIDIssued {
		return fmt.Errorf("%w: invalid issue transition", ErrJournalCorrupt)
	}
	cid, err := hex.DecodeString(w.CIDHex)
	if err != nil {
		return fmt.Errorf("%w: bad CID encoding", ErrJournalCorrupt)
	}
	if err := m.validateCID(cid); err != nil {
		return fmt.Errorf("%w: %v", ErrJournalCorrupt, err)
	}
	if _, exists := m.records[string(cid)]; exists {
		return fmt.Errorf("%w: duplicate CID issue", ErrJournalCorrupt)
	}
	if _, exists := m.bySeq[w.CIDSeq]; exists {
		return fmt.Errorf("%w: duplicate CID sequence", ErrJournalCorrupt)
	}
	if !m.ledger.Reserve(cid, w.At.UTC()) {
		return fmt.Errorf("%w: replayed CID value was previously used", ErrJournalRollback)
	}
	if !m.keys.HasGeneration(w.Generation) {
		return fmt.Errorf("%w: unknown key generation %d", ErrJournalCorrupt, w.Generation)
	}
	token, err := m.keys.Token(w.Generation, w.Route, cid)
	if err != nil {
		return fmt.Errorf("%w: derive token: %v", ErrJournalCorrupt, err)
	}
	if len(m.liveTokenRecordsLocked(token)) != 0 {
		return fmt.Errorf("%w: replayed token collision", ErrJournalCorrupt)
	}
	if !m.tokenLedger.Reserve(token, w.At.UTC()) {
		return fmt.Errorf("%w: replayed token value was previously issued", ErrJournalRollback)
	}
	if err := m.keys.Acquire(w.Generation); err != nil {
		return fmt.Errorf("%w: acquire key: %v", ErrJournalCorrupt, err)
	}
	rec := &cidRecord{
		cid:        cid,
		key:        string(cid),
		sequence:   w.CIDSeq,
		generation: w.Generation,
		route:      w.Route,
		token:      token,
		state:      CIDIssued,
		issuedAt:   w.At.UTC(),
	}
	m.records[rec.key] = rec
	m.bySeq[rec.sequence] = rec
	m.tokens[rec.token] = append(m.tokens[rec.token], rec)
	if rec.sequence >= m.nextCIDSeq {
		m.nextCIDSeq = rec.sequence + 1
	}
	if m.route == "" && rec.route != "" {
		m.route = rec.route
	}
	return nil
}
