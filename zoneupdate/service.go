package zoneupdate

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

var (
	ErrUnknownZone     = errors.New("zoneupdate: unknown zone")
	ErrZoneExists      = errors.New("zoneupdate: zone already registered")
	ErrEmptyRound      = errors.New("zoneupdate: empty round")
	ErrBadSignature    = errors.New("zoneupdate: bad segment signature")
	ErrWrongZone       = errors.New("zoneupdate: segment targets a different zone")
	ErrBrokenChain     = errors.New("zoneupdate: segment chain broken")
	ErrBadFinal        = errors.New("zoneupdate: invalid final segment")
	ErrVersionMismatch = errors.New("zoneupdate: round does not start at the current version")
	ErrUnknownRecord   = errors.New("zoneupdate: delete digest matches no record")
	ErrUnknownOp       = errors.New("zoneupdate: unknown operation kind")
)

// Service stores zones and applies verified update rounds. It is safe
// for concurrent use; competing rounds are serialized and exactly the
// rounds built on the current version are accepted.
type Service struct {
	mu    sync.Mutex
	zones map[string]*zoneState
}

type zoneState struct {
	master  ed25519.PublicKey
	version uint64
	records map[Digest]Record
}

// NewService returns an empty update service.
func NewService() *Service {
	return &Service{zones: make(map[string]*zoneState)}
}

// RegisterZone adds a zone anchored at version 0, authorizing master to
// sign its update rounds. initial seeds the zone contents.
func (s *Service) RegisterZone(name string, master ed25519.PublicKey, initial []Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.zones[name]; ok {
		return fmt.Errorf("%w: %q", ErrZoneExists, name)
	}
	records := make(map[Digest]Record, len(initial))
	for _, r := range initial {
		records[RecordDigest(r)] = r
	}
	s.zones[name] = &zoneState{master: master, records: records}
	return nil
}

// Version returns the current version of a zone.
func (s *Service) Version(zone string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	return z.version, nil
}

// Records returns the zone's records sorted by name for deterministic
// inspection by callers and tests.
func (s *Service) Records(zone string) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	records := slices.Collect(maps.Values(z.records))
	slices.SortFunc(records, func(a, b Record) int {
		if a.Name != b.Name {
			return cmpString(a.Name, b.Name)
		}
		return cmpString(a.RType, b.RType)
	})
	return records, nil
}

// Submit verifies a complete round against the zone's current state and,
// only if the whole chain is valid, publishes it atomically. Any failure
// leaves the zone untouched: no record is added or deleted.
func (s *Service) Submit(zone string, segs []*Segment) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	next, err := verifyRound(zone, z, segs)
	if err != nil {
		return 0, err
	}
	z.records = next.records
	z.version = next.version
	return z.version, nil
}

// verifyRound checks the full chain and replays its operations against a
// scratch copy of the zone. It never mutates z.
func verifyRound(zone string, z *zoneState, segs []*Segment) (*zoneState, error) {
	if len(segs) == 0 {
		return nil, ErrEmptyRound
	}
	if segs[0].StartVer != z.version {
		return nil, fmt.Errorf("%w: round starts at %d, zone is at %d",
			ErrVersionMismatch, segs[0].StartVer, z.version)
	}

	var prev Digest
	for i, seg := range segs {
		if seg.Zone != zone {
			return nil, fmt.Errorf("%w: segment %d is for %q", ErrWrongZone, i, seg.Zone)
		}
		if want := uint64(i) + 1; seg.Seq != want {
			return nil, fmt.Errorf("%w: segment %d has seq %d, want %d",
				ErrBrokenChain, i, seg.Seq, want)
		}
		if seg.PrevDigest != prev {
			return nil, fmt.Errorf("%w: segment %d prev digest mismatch", ErrBrokenChain, i)
		}
		if want := z.version + uint64(i); seg.StartVer != want || seg.TargetVer != want+1 {
			return nil, fmt.Errorf("%w: segment %d versions %d->%d, want %d->%d",
				ErrBrokenChain, i, seg.StartVer, seg.TargetVer, want, want+1)
		}
		if seg.Final && i != len(segs)-1 {
			return nil, fmt.Errorf("%w: segment %d marked final before end of chain", ErrBadFinal, i)
		}
		if !ed25519.Verify(z.master, seg.signingPayload(), seg.Signature) {
			return nil, fmt.Errorf("%w: segment %d", ErrBadSignature, i)
		}
		prev = seg.contentDigest()
	}

	last := segs[len(segs)-1]
	if !last.Final {
		return nil, fmt.Errorf("%w: chain has no final segment", ErrBadFinal)
	}
	if last.SegCount != uint64(len(segs)) {
		return nil, fmt.Errorf("%w: final commits to %d segments, got %d",
			ErrBadFinal, last.SegCount, len(segs))
	}
	if want := roundDigest(zone, z.version, segs); last.RoundDigest != want {
		return nil, fmt.Errorf("%w: round digest mismatch", ErrBadFinal)
	}

	// Replay operations on a clone so a failed round cannot delete or
	// add records in the live zone.
	records := maps.Clone(z.records)
	for i, seg := range segs {
		for j, op := range seg.Ops {
			switch op.Kind {
			case OpAdd:
				records[RecordDigest(op.Record)] = op.Record
			case OpDelete:
				if _, ok := records[op.Digest]; !ok {
					return nil, fmt.Errorf("%w: segment %d op %d", ErrUnknownRecord, i, j)
				}
				delete(records, op.Digest)
			default:
				return nil, fmt.Errorf("%w: segment %d op %d kind %d",
					ErrUnknownOp, i, j, op.Kind)
			}
		}
	}
	return &zoneState{master: z.master, version: last.TargetVer, records: records}, nil
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
