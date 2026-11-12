package taskpkg

import (
	"crypto/ed25519"
	"fmt"
	"sync"
)

// Store is the authoritative, concurrency-safe state of one zone. It accepts a
// submitted round only when the complete chain verifies against the current
// version and every operation is consistent with the live records.
type Store struct {
	mu sync.Mutex

	zone    ZoneID
	version Version
	records map[string]Record
	masters []ed25519.PublicKey
}

// NewStore creates a zone store seeded with records and the public keys of the
// master nodes allowed to sign updates. The initial version is typically zero.
func NewStore(zone ZoneID, initial Version, records []Record, masters []ed25519.PublicKey) *Store {
	stored := make(map[string]Record, len(records))
	for _, rec := range records {
		stored[rec.Key] = rec
	}

	authorized := make([]ed25519.PublicKey, len(masters))
	copy(authorized, masters)

	return &Store{
		zone:    zone,
		version: initial,
		records: stored,
		masters: authorized,
	}
}

// Zone reports the zone identity served by the store.
func (s *Store) Zone() ZoneID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.zone
}

// Version reports the currently published version.
func (s *Store) Version() Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Lookup returns a copy of the record under key and whether it exists.
func (s *Store) Lookup(key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	if !ok {
		return Record{}, false
	}
	rec.Value = append([]byte(nil), rec.Value...)
	return rec, true
}

// Apply validates and, if valid, publishes an update round.
//
// Verification happens entirely before publication: the full chain is checked
// structurally and cryptographically, then operations are replayed on a
// staging copy of the records. The live map is replaced only after every step
// succeeds, so a rejected round leaves every existing record untouched and
// never deletes old data.
func (s *Store) Apply(segments []Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.verifyChainLocked(segments); err != nil {
		return err
	}

	// Replay on a staging copy. A failure here discards the copy and the
	// committed state is unchanged.
	staging := make(map[string]Record, len(s.records))
	for key, rec := range s.records {
		staging[key] = rec
	}

	for _, seg := range segments {
		for _, op := range seg.Operations {
			switch op.Type {
			case OpUpsert:
				staging[op.Key] = Record{
					Key:   op.Key,
					Value: append([]byte(nil), op.Value...),
				}
			case OpDelete:
				rec, ok := staging[op.Key]
				if !ok || rec.Digest() != op.Digest {
					return fmt.Errorf("%w: key %q", ErrDeleteMismatch, op.Key)
				}
				delete(staging, op.Key)
			default:
				return ErrInvalidSegment
			}
		}
	}

	s.records = staging
	s.version = segments[0].TargetVersion
	return nil
}

// VerifyChain performs the structural and cryptographic checks of a round
// without touching any records. It is exported so a name service can
// pre-validate a round, for example before forwarding it to the publisher.
func (s *Store) VerifyChain(segments []Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyChainLocked(segments)
}

// verifyChainLocked checks every contextual binding in order. It does not
// mutate state.
func (s *Store) verifyChainLocked(segments []Segment) error {
	if len(segments) == 0 {
		return ErrEmptyRound
	}

	var (
		prev     [32]byte
		seenSize = uint64(len(segments))
		start    Version
		target   Version
	)

	for index, seg := range segments {
		if seg.Index != uint64(index) {
			return fmt.Errorf("%w: got index %d at position %d", ErrSegmentIndex, seg.Index, index)
		}

		if seg.Zone != s.zone {
			return fmt.Errorf("%w: segment zone %q, store zone %q", ErrZoneMismatch, seg.Zone, s.zone)
		}

		if index == 0 {
			start = seg.StartVersion
			target = seg.TargetVersion
			if start != s.version {
				return fmt.Errorf("%w: round starts at %d, current is %d", ErrVersionMismatch, start, s.version)
			}
			if target <= start {
				return ErrVersionMismatch
			}
		} else if seg.StartVersion != start || seg.TargetVersion != target {
			return fmt.Errorf("%w: segment %d disagrees on round versions", ErrVersionMismatch, index)
		}

		if seg.PrevDigest != prev {
			return fmt.Errorf("%w: segment %d predecessor does not match", ErrChainLink, index)
		}

		if err := validateOperations(seg.Operations); err != nil {
			return err
		}

		if !s.verifySignatureLocked(seg) {
			return fmt.Errorf("%w: segment %d", ErrSignature, index)
		}

		isFinal := index == len(segments)-1
		if seg.Final != isFinal {
			return fmt.Errorf("%w: segment %d final flag is %v", ErrFinalSegment, index, seg.Final)
		}

		prev = segmentDigest(seg)
	}

	final := segments[len(segments)-1]
	if final.RoundSize != seenSize {
		return fmt.Errorf("%w: signed count %d, actual %d", ErrRoundDigest, final.RoundSize, seenSize)
	}

	wantRound := roundDigest(s.zone, start, target, segments)
	if final.RoundDigest != wantRound {
		return ErrRoundDigest
	}

	return nil
}

// verifySignatureLocked accepts a signature that any authorized master key
// verifies. A valid ed25519 signature from an unknown key is still rejected.
func (s *Store) verifySignatureLocked(seg Segment) bool {
	input := segmentSigningInput(seg)
	for _, key := range s.masters {
		if ed25519.Verify(key, input, seg.Signature) {
			return true
		}
	}
	return false
}

func validateOperations(ops []Operation) error {
	for _, op := range ops {
		switch op.Type {
		case OpUpsert, OpDelete:
		default:
			return fmt.Errorf("%w: unknown operation type %d", ErrInvalidSegment, op.Type)
		}
		if op.Key == "" {
			return ErrInvalidSegment
		}
	}
	return nil
}
