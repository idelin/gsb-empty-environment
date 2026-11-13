package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type txGroup struct {
	id      TxID
	records []Record
	commit  *Record
}

func (g *txGroup) commitRecord() *Record {
	return g.commit
}

func (h *Host) recoverLocked(ctx context.Context) (RecoveryReport, error) {
	rep := RecoveryReport{
		CheckpointFound: h.cpFound,
		CheckpointSeq:   h.cp.Seq,
		TornTail:        h.j.torn,
	}
	seq, _ := h.j.position()
	rep.TrustedThroughSeq = seq
	var errs []error
	if h.cpIssue != nil {
		rep.Errors = append(rep.Errors, h.cpIssue.Error())
	}
	if _, err := h.j.health(); err != nil {
		rep.Isolated = true
		rep.Errors = append(rep.Errors, err.Error())
		errs = append(errs, err)
	}

	startSeq := uint64(0)
	if h.cpFound {
		if h.cp.Seq == 0 {
			if !h.cp.Head.IsZero() {
				err := fmt.Errorf("%w: genesis checkpoint has non-zero head", ErrCheckpointInvalid)
				rep.Errors = append(rep.Errors, err.Error())
				errs = append(errs, err)
				h.report = rep
				h.recoveryErr = errors.Join(errs...)
				return rep, errors.Join(errs...)
			}
		} else {
			frame, ok := h.j.frameBySeq(h.cp.Seq)
			if !ok || frame.digest != h.cp.Head {
				err := fmt.Errorf("%w: checkpoint seq=%d", ErrCheckpointMissing, h.cp.Seq)
				rep.Errors = append(rep.Errors, err.Error())
				errs = append(errs, err)
				h.report = rep
				h.recoveryErr = errors.Join(errs...)
				return rep, errors.Join(errs...)
			}
			startSeq = h.cp.Seq
		}
	}

	groups, order := groupRecords(h.j.trustedFrames())

	// Roll back every transaction that has any record after the trusted
	// checkpoint but no complete Commit.  This does not inspect credentials
	// or policy.
	for _, id := range order {
		g := groups[id]
		if !groupExtendsPast(g, startSeq) {
			continue
		}
		if g.commitRecord() == nil {
			if err := h.store.Rollback(ctx, id); err != nil {
				rep.Conflicts = append(rep.Conflicts, id)
				rep.Errors = append(rep.Errors, fmt.Sprintf("rollback %s: %v", id, err))
				errs = append(errs, fmt.Errorf("%w: rollback %s: %v", ErrRecoveryConflict, id, err))
				continue
			}
			rep.RolledBack = append(rep.RolledBack, id)
		}
	}

	committed := make([]*txGroup, 0)
	for _, id := range order {
		g := groups[id]
		if g.commitRecord() == nil {
			continue
		}
		if g.commitRecord().Seq > startSeq {
			committed = append(committed, g)
		}
	}
	sort.SliceStable(committed, func(i, j int) bool {
		return committed[i].commitRecord().Seq < committed[j].commitRecord().Seq
	})
	for _, g := range committed {
		if err := h.completeCommitted(ctx, g); err != nil {
			rep.Conflicts = append(rep.Conflicts, g.id)
			rep.Errors = append(rep.Errors, fmt.Sprintf("complete %s: %v", g.id, err))
			errs = append(errs, fmt.Errorf("%w: %s: %v", ErrRecoveryConflict, g.id, err))
			continue
		}
		rep.Completed = append(rep.Completed, g.id)
	}
	if cleaner, ok := h.store.(StagingCleaner); ok {
		keep := make(map[TxID]struct{}, len(groups))
		for id := range groups {
			keep[id] = struct{}{}
		}
		if err := cleaner.CleanupOrphans(ctx, keep); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("cleanup staging: %v", err))
			errs = append(errs, fmt.Errorf("netsec: cleanup staging: %w", err))
		}
	}

	h.report = rep
	h.refreshHealth()
	healthOK, healthErr := h.j.health()
	h.healthy, h.healthErr = healthOK, healthErr
	h.recoveryErr = errors.Join(errs...)
	return rep, errors.Join(errs...)
}

func groupRecords(frames []journalFrame) (map[TxID]*txGroup, []TxID) {
	groups := make(map[TxID]*txGroup)
	order := make([]TxID, 0)
	for _, frame := range frames {
		rec := frame.record
		g, ok := groups[rec.Identity.TxID]
		if !ok {
			g = &txGroup{id: rec.Identity.TxID}
			groups[rec.Identity.TxID] = g
			order = append(order, rec.Identity.TxID)
		}
		g.records = append(g.records, rec)
		if rec.Stage == StageCommit {
			r := rec
			g.commit = &r
		}
	}
	return groups, order
}

func groupExtendsPast(g *txGroup, seq uint64) bool {
	for _, rec := range g.records {
		if rec.Seq > seq {
			return true
		}
	}
	return false
}

// completeCommitted is the only recovery path that can publish data.  It has
// no Authorizer or credential lookup by design.  A commit is completed only
// if the object still has the recorded generation and its metadata digest is
// either the recorded old digest or the already-applied new digest.
func (h *Host) completeCommitted(ctx context.Context, g *txGroup) error {
	commit := *g.commitRecord()
	id := commit.Identity
	ref := ObjectRef{ID: id.ObjectID, Generation: id.ObjectGeneration}

	currentGen, err := h.store.CurrentGeneration(ctx, id.ObjectID)
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	if currentGen != id.ObjectGeneration {
		if marker, ok := h.store.(AppliedMarker); ok {
			applied, err := marker.WasApplied(ctx, ref, g.id, id.NewMetadataDigest)
			if err != nil {
				return err
			}
			if applied {
				// The transaction was committed and published to the
				// old generation before the object number was reused.
				// It must not be migrated to the new generation, but it
				// is not a conflict either.
				return h.store.Rollback(ctx, g.id)
			}
		}
		return fmt.Errorf("%w: object %q current=%d transaction=%d", ErrObjectGenerationChanged, id.ObjectID, currentGen, id.ObjectGeneration)
	}
	snapshot, err := h.store.Read(ctx, ref)
	if err != nil {
		return err
	}
	currentDigest := snapshot.MetadataDigest

	switch {
	case currentDigest == id.NewMetadataDigest:
		if id.DataDigest != ZeroDigest {
			if err := h.store.PublishData(ctx, g.id, ref, id.DataDigest); err != nil {
				return err
			}
		}
		if marker, ok := h.store.(AppliedMarker); ok {
			if err := marker.MarkApplied(ctx, ref, g.id, id.NewMetadataDigest); err != nil {
				return err
			}
		}
		if err := h.store.Rollback(ctx, g.id); err != nil {
			return err
		}
		return nil

	case currentDigest == id.OldMetadataDigest:
		newMetadata, err := h.applier.Apply(snapshot.Metadata, commit.Patch)
		if err != nil {
			return err
		}
		if got := DigestBytes(newMetadata); got != id.NewMetadataDigest {
			return fmt.Errorf("%w: reapplied patch digest %s, want %s", ErrInvalidRecord, got, id.NewMetadataDigest)
		}
		if id.DataDigest != ZeroDigest {
			if err := h.store.PublishData(ctx, g.id, ref, id.DataDigest); err != nil {
				return err
			}
		}
		if err := h.store.PublishMetadata(ctx, g.id, ref, id.OldMetadataDigest, id.NewMetadataDigest, newMetadata); err != nil {
			return err
		}
		if err := h.store.Rollback(ctx, g.id); err != nil {
			return err
		}
		return nil

	default:
		return fmt.Errorf("%w: metadata is neither old %s nor new %s", ErrMetadataConflict, id.OldMetadataDigest, id.NewMetadataDigest)
	}
}
