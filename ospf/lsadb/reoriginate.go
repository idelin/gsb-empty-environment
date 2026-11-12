package lsadb

// DefaultReoriginate is the default ReoriginateFunc. It rebuilds the LSA the
// local router would have originated, preserving the header identity (type,
// Link State ID, options and body) while resetting LS age to zero and setting
// the requested new sequence number. The Fletcher checksum and wire bytes are
// recomputed by Marshal.
//
// When no previous local instance exists the forged LSA's own identity and
// body are reused, so the counter-originated LSA still describes the same
// link-state item the attacker tried to hijack.
func DefaultReoriginate(prev, incoming *LSA, newSeq int32) (*LSA, error) {
	template := prev
	if template == nil {
		template = incoming
	}

	body := make([]byte, len(template.Body))
	copy(body, template.Body)

	fresh := &LSA{
		LSAGE:             0,
		Options:           template.Options,
		LSType:            template.LSType,
		LinkStateID:       template.LinkStateID,
		AdvertisingRouter: incoming.AdvertisingRouter,
		SeqNum:            newSeq,
		Body:              body,
	}
	if _, err := fresh.Marshal(); err != nil {
		return nil, err
	}
	return fresh, nil
}

// maxAgeCopy returns a MaxAge-marked copy of l for flushing. The copy keeps
// the same header identity and sequence number and gets a recomputed
// checksum (RFC 2328 section 14.1).
func maxAgeCopy(l *LSA) *LSA {
	body := make([]byte, len(l.Body))
	copy(body, l.Body)

	flush := &LSA{
		LSAGE:             MaxAge,
		Options:           l.Options,
		LSType:            l.LSType,
		LinkStateID:       l.LinkStateID,
		AdvertisingRouter: l.AdvertisingRouter,
		SeqNum:            l.SeqNum,
		Body:              body,
	}
	// Marshaling cannot fail for a copy of an already valid LSA.
	_, _ = flush.Marshal()
	return flush
}
