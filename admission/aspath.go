package admission

import "fmt"

// SegmentType mirrors the AS_PATH segment types defined in RFC 4271 section 5.1.2.
type SegmentType uint8

const (
	ASSequence SegmentType = 1 // AS_SEQUENCE
	ASSet      SegmentType = 2 // AS_SET
	// AS_CONFED_SEQUENCE (3) and AS_CONFED_SET (4) are intra-AS constructs and
	// are ignored when validating routes received from eBGP peers.
	ASConfedSequence SegmentType = 3
	ASConfedSet      SegmentType = 4
)

// Segment is one AS_PATH segment.
type Segment struct {
	Type SegmentType
	ASes []uint32
}

// ASPath is the full AS_PATH attribute of an UPDATE.
type ASPath struct {
	Segments []Segment
}

// Seq builds an AS_SEQUENCE segment.
func Seq(ases ...uint32) Segment { return Segment{Type: ASSequence, ASes: ases} }

// Set builds an AS_SET segment.
func Set(ases ...uint32) Segment { return Segment{Type: ASSet, ASes: ases} }

// Path assembles an ASPath from segments.
func Path(segments ...Segment) ASPath { return ASPath{Segments: segments} }

// MaxPrepend is the maximum number of consecutive occurrences of the same AS
// allowed inside AS_SEQUENCE data (an AS may be prepended at most twice on
// top of its single natural occurrence).
const MaxPrepend = 3

type violation struct {
	reason Reason
	detail string
}

// validatePath enforces the AS_PATH side of the import policy:
//   - eBGP sessions reject an empty AS_PATH;
//   - on eBGP sessions the first AS of the leading AS_SEQUENCE must be the peer AS;
//   - private or reserved AS numbers are rejected in every non-confederation segment;
//   - no AS may appear more than MaxPrepend times consecutively in AS_SEQUENCE data.
//
// It also derives the route origin. When the final non-confederation segment is
// an AS_SET the origin cannot be determined (RFC 6811) and originKnown is false.
func validatePath(sess Session, path ASPath) (origin uint32, originKnown bool, violations []violation) {
	var nonConfed []Segment
	for _, seg := range path.Segments {
		if seg.Type == ASConfedSequence || seg.Type == ASConfedSet {
			continue
		}
		nonConfed = append(nonConfed, seg)
	}

	var flat []uint32
	var firstSeqAS uint32
	haveFirstSeq := false
	var last Segment
	haveLast := false

	runAS, runLen := uint32(0), 0
	flushRun := func() {
		if runLen > MaxPrepend {
			violations = append(violations, violation{
				reason: ReasonPrependExceeded,
				detail: fmt.Sprintf("AS %d appears %d consecutive times, limit is %d", runAS, runLen, MaxPrepend),
			})
		}
		runLen = 0
	}

	for _, seg := range nonConfed {
		if len(seg.ASes) == 0 {
			continue
		}
		haveLast = true
		last = seg
		flat = append(flat, seg.ASes...)

		switch seg.Type {
		case ASSequence:
			if !haveFirstSeq {
				firstSeqAS = seg.ASes[0]
				haveFirstSeq = true
			}
			for _, as := range seg.ASes {
				if runLen > 0 && as == runAS {
					runLen++
				} else {
					flushRun()
					runAS, runLen = as, 1
				}
			}
		case ASSet:
			// An AS_SET is an unordered collection, so it breaks any prepend run.
			flushRun()
		}
	}
	flushRun()

	if sess.EBGP && len(flat) == 0 {
		violations = append(violations, violation{
			reason: ReasonEmptyPathOnEBGP,
			detail: "eBGP UPDATE carries an empty AS_PATH",
		})
	}

	if sess.EBGP {
		switch {
		case !haveFirstSeq:
			violations = append(violations, violation{
				reason: ReasonFirstASMismatch,
				detail: "AS_PATH has no leading AS_SEQUENCE, so the peer AS cannot be verified",
			})
		case firstSeqAS != sess.PeerAS:
			violations = append(violations, violation{
				reason: ReasonFirstASMismatch,
				detail: fmt.Sprintf("first AS %d does not match session peer AS %d", firstSeqAS, sess.PeerAS),
			})
		}
	}

	for _, as := range flat {
		if category, ok := classifyAS(as); ok {
			reason := ReasonReservedAS
			if category == "private" {
				reason = ReasonPrivateAS
			}
			violations = append(violations, violation{
				reason: reason,
				detail: fmt.Sprintf("AS %d is a %s AS number", as, category),
			})
		}
	}

	switch {
	case !haveLast:
		originKnown = false
	case last.Type == ASSet:
		// RFC 6811: the route origin is undetermined (NONE) when the final
		// segment is an AS_SET.
		originKnown = false
	default:
		origin = last.ASes[len(last.ASes)-1]
		originKnown = true
	}
	return origin, originKnown, violations
}

// classifyAS reports private and reserved AS numbers. Documentation ranges
// (RFC 5398, RFC 64496-64511 and RFC 6790 test blocks) are deliberately not
// blocked; the RFC 5398 range is the pool used for real peering assignments.
func classifyAS(as uint32) (category string, blocked bool) {
	switch {
	case as == 0, as == 65535, as == 4294967295:
		return "reserved", true
	case as == 23456:
		return "reserved", true // AS_TRANS, RFC 6793
	case as >= 65552 && as <= 131071:
		return "reserved", true // IANA-reserved 32-bit block
	case as >= 64512 && as <= 65534:
		return "private", true // RFC 6996, 16-bit private range
	case as >= 4200000000 && as <= 4294967294:
		return "private", true // RFC 6996, 32-bit private range
	default:
		return "", false
	}
}
