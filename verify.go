package netsec

import (
	"errors"
	"fmt"
)

// Verify independently checks a bundle's integrity: chain linkage, record
// digests, snapshot consistency, protocol step order, seal, and execution
// semantics. Any missing step, reordering, cross-snapshot splice, or
// success claim without a completed successful exit is rejected.
func Verify(b *Bundle) error {
	if b == nil {
		return errors.New("evidence: nil bundle")
	}
	if b.Version != 1 {
		return fmt.Errorf("evidence: unsupported version %d", b.Version)
	}
	if len(b.Records) < 2 {
		return errors.New("evidence: bundle too short: need at least snapshot and seal")
	}

	prev := zeroDigest
	for i, rec := range b.Records {
		if rec.Seq != i {
			return fmt.Errorf("evidence: record %d: seq is %d, chain broken or reordered", i, rec.Seq)
		}
		if rec.Prev != prev {
			return fmt.Errorf("evidence: record %d: prev link mismatch, chain broken", i)
		}
		if rec.Digest != rec.computeDigest() {
			return fmt.Errorf("evidence: record %d: digest mismatch, content tampered", i)
		}
		if rec.Snapshot != b.Snapshot {
			return fmt.Errorf("evidence: record %d: snapshot %s does not match bundle snapshot %s (cross-snapshot splice)", i, rec.Snapshot, b.Snapshot)
		}
		prev = rec.Digest
	}

	if err := verifyProtocol(b); err != nil {
		return err
	}

	last := b.Records[len(b.Records)-1]
	if b.Head != last.Digest {
		return errors.New("evidence: bundle head does not match final record digest")
	}
	return nil
}

// VerifyAgainstSource additionally recomputes the source digest of root and
// requires it to equal the bundle's snapshot, proving the recorded runs were
// against this exact code snapshot.
func VerifyAgainstSource(b *Bundle, root string) error {
	if err := Verify(b); err != nil {
		return err
	}
	sd, err := SourceDigest(root)
	if err != nil {
		return err
	}
	if sd != b.Snapshot {
		return fmt.Errorf("evidence: source at %s does not match bundle snapshot", root)
	}
	return nil
}

func verifyProtocol(b *Bundle) error {
	const (
		stSnapshot = iota
		stTestOrSeal
		stInput
		stExecStart
		stExecEnd
		stDone
	)
	state := stSnapshot
	var testDigest, inputDigest string

	for i, rec := range b.Records {
		switch state {
		case stSnapshot:
			if rec.Kind != KindSnapshot {
				return fmt.Errorf("evidence: record %d: expected snapshot anchor, got %q", i, rec.Kind)
			}
			if rec.Payload.SourceDigest != b.Snapshot {
				return fmt.Errorf("evidence: record %d: snapshot payload does not match bundle snapshot", i)
			}
			state = stTestOrSeal
		case stTestOrSeal:
			switch rec.Kind {
			case KindTest:
				if rec.Payload.TestDigest == "" {
					return fmt.Errorf("evidence: record %d: test record missing test digest", i)
				}
				testDigest = rec.Payload.TestDigest
				state = stInput
			case KindSeal:
				if i != len(b.Records)-1 {
					return fmt.Errorf("evidence: record %d: seal must be the final record", i)
				}
				if rec.Payload.Head != b.Records[i-1].Digest {
					return fmt.Errorf("evidence: record %d: seal head does not match previous record", i)
				}
				state = stDone
			default:
				return fmt.Errorf("evidence: record %d: expected test or seal, got %q (missing step)", i, rec.Kind)
			}
		case stInput:
			if rec.Kind != KindInput {
				return fmt.Errorf("evidence: record %d: expected input record after test, got %q (missing step)", i, rec.Kind)
			}
			if rec.Payload.InputDigest == "" {
				return fmt.Errorf("evidence: record %d: input record missing input digest", i)
			}
			inputDigest = rec.Payload.InputDigest
			state = stExecStart
		case stExecStart:
			if rec.Kind != KindExecStart {
				return fmt.Errorf("evidence: record %d: expected exec_start after input, got %q (missing step)", i, rec.Kind)
			}
			if rec.Payload.Start.IsZero() {
				return fmt.Errorf("evidence: record %d: exec_start missing start time", i)
			}
			if rec.Payload.TestDigest != testDigest || rec.Payload.InputDigest != inputDigest {
				return fmt.Errorf("evidence: record %d: exec_start does not reference the current test/input", i)
			}
			state = stExecEnd
		case stExecEnd:
			if rec.Kind != KindExecEnd {
				return fmt.Errorf("evidence: record %d: execution left unfinished, expected exec_end, got %q", i, rec.Kind)
			}
			if err := verifyExecEnd(rec); err != nil {
				return err
			}
			if rec.Payload.TestDigest != testDigest || rec.Payload.InputDigest != inputDigest {
				return fmt.Errorf("evidence: record %d: exec_end does not reference the current test/input", i)
			}
			state = stTestOrSeal
		case stDone:
			return fmt.Errorf("evidence: record %d: record after seal", i)
		}
	}
	if state != stDone {
		return errors.New("evidence: bundle truncated: missing seal or unfinished execution")
	}
	return nil
}

func verifyExecEnd(rec Record) error {
	p := rec.Payload
	if p.Start.IsZero() || p.End.IsZero() {
		return fmt.Errorf("evidence: record %d: exec_end missing start/end time", rec.Seq)
	}
	if p.End.Before(p.Start) {
		return fmt.Errorf("evidence: record %d: exec_end end time before start time", rec.Seq)
	}
	switch p.Status {
	case StatusPassed:
		if p.ExitCode == nil || *p.ExitCode != 0 || p.Signal != "" {
			return fmt.Errorf("evidence: record %d: status passed requires a completed exit code 0 and no signal", rec.Seq)
		}
	case StatusFailed, StatusInterrupted:
	default:
		return fmt.Errorf("evidence: record %d: unknown status %q", rec.Seq, p.Status)
	}
	return nil
}
