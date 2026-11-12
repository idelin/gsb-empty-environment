package netsec

import (
	"bytes"
	"fmt"
	"io"
	"time"
)

// eraseRegion overwrites every 1 MiB stripe with the fixed pattern, reads it
// back, and journals the stripe checkpoint only after a successful match.  A
// conditional complementary-pattern audit also defeats devices that silently
// drop writes when the old bytes happen to equal the target pattern.  A stripe
// with a checkpoint is re-read on a later attempt; if the read-back no longer
// matches, it is rewritten rather than trusted blindly.  The clean record is
// appended only after a final all-stripe gate.
func (m *Manager) eraseRegion(r *region) error {
	if r.state != StateErasing {
		return fmt.Errorf("%w: region %q is %s", ErrNotErasing, r.id, r.state)
	}
	if r.stripes <= 0 {
		return fmt.Errorf("%w: region %q has no stripes", ErrIncompleteErase, r.id)
	}

	writeBuf := make([]byte, StripeSize)
	for i := range writeBuf {
		writeBuf[i] = m.cfg.Pattern
	}
	// If a stripe already contains the target pattern (for example, blank
	// persistent memory that reads as zero), a dropped write followed by a
	// matching read-back is indistinguishable from a successful overwrite.
	// In that case write the complementary pattern, verify it, then write the
	// configured pattern and verify again.  A device that drops either pass
	// can no longer masquerade as a completed stripe.
	auditBuf := make([]byte, StripeSize)
	for i := range auditBuf {
		auditBuf[i] = ^m.cfg.Pattern
	}
	readBuf := make([]byte, StripeSize)

	for stripe := int64(0); stripe < r.stripes; stripe++ {
		if r.verified[stripe] {
			match, err := m.readMatches(r.id, stripe, readBuf, writeBuf)
			if err == nil && match {
				continue
			}
			// A checkpoint is a promise that the stripe was verified once,
			// not proof that the backing memory retained it.  Fall through and
			// rewrite the stripe.
		}

		if m.faults.BeforeStripe != nil {
			if err := m.faults.BeforeStripe(r.id, stripe); err != nil {
				return fmt.Errorf("region %q stripe %d before write: %w", r.id, stripe, err)
			}
		}
		preMatches, preErr := m.readMatches(r.id, stripe, readBuf, writeBuf)
		if preErr != nil || preMatches {
			// On a read error we conservatively run the audit pass instead of
			// assuming the stripe is safe to overwrite only once.
			n, err := m.device.WriteStripe(r.id, stripe, auditBuf)
			if err != nil {
				return fmt.Errorf("region %q stripe %d audit write: %w", r.id, stripe, err)
			}
			if n != len(auditBuf) {
				return fmt.Errorf("region %q stripe %d audit short write %d/%d: %w", r.id, stripe, n, len(auditBuf), io.ErrShortWrite)
			}
			auditMatches, err := m.readMatches(r.id, stripe, readBuf, auditBuf)
			if err != nil {
				return fmt.Errorf("region %q stripe %d audit read-back: %w", r.id, stripe, err)
			}
			if !auditMatches {
				return fmt.Errorf("%w: region %q stripe %d audit pattern", ErrVerificationFailed, r.id, stripe)
			}
		}
		n, err := m.device.WriteStripe(r.id, stripe, writeBuf)
		if err != nil {
			return fmt.Errorf("region %q stripe %d write: %w", r.id, stripe, err)
		}
		if n != len(writeBuf) {
			return fmt.Errorf("region %q stripe %d short write %d/%d: %w", r.id, stripe, n, len(writeBuf), io.ErrShortWrite)
		}
		if m.faults.AfterWrite != nil {
			if err := m.faults.AfterWrite(r.id, stripe); err != nil {
				return fmt.Errorf("region %q stripe %d after write: %w", r.id, stripe, err)
			}
		}

		match, err := m.readMatches(r.id, stripe, readBuf, writeBuf)
		if err != nil {
			return fmt.Errorf("region %q stripe %d read-back: %w", r.id, stripe, err)
		}
		if !match {
			return fmt.Errorf("%w: region %q stripe %d with pattern 0x%02x", ErrVerificationFailed, r.id, stripe, m.cfg.Pattern)
		}
		if m.faults.AfterVerify != nil {
			if err := m.faults.AfterVerify(r.id, stripe); err != nil {
				return fmt.Errorf("region %q stripe %d after verify: %w", r.id, stripe, err)
			}
		}

		// Persist the checkpoint before considering the stripe done.  A power
		// cut after this append resumes at the next stripe; a cut before it
		// repeats this stripe from the beginning.
		if err := m.journal.append(journalEntry{
			Kind:   kindStripeChecked,
			Region: r.id,
			Stripe: stripe,
		}); err != nil {
			return err
		}
		r.verified[stripe] = true
	}

	if m.faults.BeforeMarkClean != nil {
		if err := m.faults.BeforeMarkClean(r.id); err != nil {
			return fmt.Errorf("region %q before mark clean: %w", r.id, err)
		}
	}
	if err := m.verifyAllStripes(r, readBuf, writeBuf); err != nil {
		return err
	}
	if err := m.journal.append(journalEntry{Kind: kindClean, Region: r.id}); err != nil {
		return err
	}
	r.state = StateClean
	r.tenant = ""
	r.allocatedAt = time.Time{}
	r.verified = nil
	return nil
}

func (m *Manager) readMatches(regionID string, stripe int64, readBuf, want []byte) (bool, error) {
	// Do not let stale bytes from a previous stripe make a device that
	// under-fills the buffer look successful.
	clear(readBuf)
	n, err := m.device.ReadStripe(regionID, stripe, readBuf)
	if err != nil {
		return false, err
	}
	if n != len(want) {
		return false, io.ErrUnexpectedEOF
	}
	return bytes.Equal(readBuf, want), nil
}

// verifyAllStripes is the final fail-closed gate.  Every stripe must have a
// durable verified checkpoint and must still read back as the fixed pattern.
// This catches both a skipped stripe and a checkpoint whose bytes were lost.
func (m *Manager) verifyAllStripes(r *region, readBuf, want []byte) error {
	if int64(len(r.verified)) != r.stripes {
		return fmt.Errorf("%w: region %q has %d/%d verified checkpoints", ErrIncompleteErase, r.id, len(r.verified), r.stripes)
	}
	for stripe := int64(0); stripe < r.stripes; stripe++ {
		if !r.verified[stripe] {
			return fmt.Errorf("%w: region %q stripe %d was never verified", ErrIncompleteErase, r.id, stripe)
		}
		match, err := m.readMatches(r.id, stripe, readBuf, want)
		if err != nil {
			return fmt.Errorf("region %q stripe %d final read-back: %w", r.id, stripe, err)
		}
		if !match {
			return fmt.Errorf("%w: region %q stripe %d during final gate", ErrVerificationFailed, r.id, stripe)
		}
	}
	return nil
}
