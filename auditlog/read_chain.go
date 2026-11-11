package auditlog

import (
	"crypto/hmac"
	"fmt"
)

func verifyChain(segments []parsedSegment) (*Report, error) {
	report := &Report{}
	var prevSealDigest []byte
	var prevRecordDigest []byte
	expectedSeq := uint64(1)
	var lastRecordDigest []byte

	for i, segment := range segments {
		status := segment.status
		isTerminal := i == len(segments)-1

		if i > 0 && status.Index != segments[i-1].status.Index+1 {
			return nil, tamper(TamperOrder, status.Index, 0, status.File, 0,
				fmt.Sprintf("segment index gap; expected %d", segments[i-1].status.Index+1))
		}

		switch {
		case status.Sealed:
			wantSeal := prevSealDigest
			if wantSeal == nil {
				wantSeal = genesisDigest[:]
			}
			if !hmac.Equal(segment.sealPrevDigest, wantSeal) {
				return nil, tamper(TamperChain, status.Index, 0, status.File, 0,
					"seal predecessor digest does not match previous seal")
			}
		case isTerminal:
			// The only legitimate live unsealed segment: either a clean open
			// segment or one carrying a torn power-loss tail.
		default:
			// Non-terminal unsealed segments are legal only as preserved
			// torn-tail evidence later superseded by a recovery segment.
			if segment.torn == nil {
				return nil, tamper(TamperOrder, status.Index, 0, status.File, 0,
					"clean unsealed segment has a successor segment")
			}
		}

		// Residue segments are authenticated by the per-segment parser but do
		// not participate in global sequence continuity: their records were
		// never sealed and their successors reuse the same chain state. A torn
		// terminal segment is residue too, because the writer always rolls a
		// fresh segment after observing its tail.
		residue := segment.torn != nil
		participates := (status.Sealed || isTerminal) && !residue

		// Residue segments still consume sequence numbers at write time, so
		// the next participating segment continues after their last record.
		if residue && len(segment.records) > 0 {
			expectedSeq = segment.records[len(segment.records)-1].Sequence + 1
		}

		if participates && len(segment.records) > 0 {
			chainDigest := prevRecordDigest
			if chainDigest == nil {
				chainDigest = genesisDigest[:]
			}
			if !hmac.Equal(segment.firstPrevDigest, chainDigest) {
				return nil, tamper(TamperChain, status.Index,
					segment.records[0].Sequence, status.File, 0,
					"first record does not chain into the previous record")
			}
		}

		if participates {
			for _, record := range segment.records {
				if record.Sequence != expectedSeq {
					return nil, tamper(TamperChain, status.Index, record.Sequence,
						status.File, 0, fmt.Sprintf("expected sequence %d", expectedSeq))
				}
				expectedSeq = record.Sequence + 1
				report.Records = append(report.Records, record)
			}
		}

		report.Segments = append(report.Segments, status)
		if segment.torn != nil {
			report.TornTails = append(report.TornTails, *segment.torn)
		}
		if len(segment.records) > 0 {
			last := segment.records[len(segment.records)-1].Sequence
			if last > report.LastSequence {
				report.LastSequence = last
			}
		}

		if participates || residue {
			if segment.sealDigest != nil {
				prevSealDigest = segment.sealDigest
			}
			if segment.lastDigest != nil {
				prevRecordDigest = segment.lastDigest
				lastRecordDigest = segment.lastDigest
			}
		}
	}

	report.LastRecordDigest = lastRecordDigest
	return report, nil
}
