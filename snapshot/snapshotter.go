package snapshot

import "sync"

// Snapshotter produces incremental segments and checkpoints for a Machine
// and stores them in an Archive. Capture and Checkpoint are serialized
// internally; both are safe to run concurrently with Apply.
type Snapshotter struct {
	mu   sync.Mutex
	m    *Machine
	arch Archive
}

func NewSnapshotter(m *Machine, arch Archive) *Snapshotter {
	return &Snapshotter{m: m, arch: arch}
}

// CaptureIncremental archives the mutations since the last captured segment.
// The pause is a single map swap; serialization and I/O happen off the
// critical path. If nothing changed, nothing is archived and the returned
// meta has Empty set. On archive failure the dirty set is rolled back so no
// mutation is lost.
func (s *Snapshotter) CaptureIncremental() (SegmentMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seg, done := s.m.swapDirty()
	if len(seg.Changes) == 0 {
		done(nil)
		return SegmentMeta{Base: seg.Base, Last: seg.Last, Empty: true}, nil
	}
	if err := s.arch.PutSegment(seg); err != nil {
		done(err)
		return SegmentMeta{}, err
	}
	done(nil)
	return SegmentMeta{Base: seg.Base, Last: seg.Last}, nil
}

// Checkpoint archives a full snapshot of the machine and garbage-collects
// all incremental segments fully covered by it.
func (s *Snapshotter) Checkpoint() (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.m.checkpoint()
	if err := s.arch.PutCheckpoint(cp); err != nil {
		return Checkpoint{}, err
	}
	if err := s.arch.DeleteSegmentsBefore(cp.LastIndex); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// Restore rebuilds a fresh machine from an archive: latest checkpoint plus
// the chained incremental segments on top of it. Returns ErrGap if the
// segment chain does not reach back to the checkpoint.
func Restore(arch Archive, m *Machine) error {
	cp, hasCP, err := arch.LatestCheckpoint()
	if err != nil {
		return err
	}
	base := uint64(0)
	if hasCP {
		base = cp.LastIndex
	}
	metas, err := arch.Segments()
	if err != nil {
		return err
	}
	var replay []Segment
	expect := base
	for _, meta := range metas {
		if meta.Last <= expect {
			continue // fully covered already
		}
		if meta.Base > expect {
			return ErrGap
		}
		seg, err := arch.GetSegment(meta.Base)
		if err != nil {
			return err
		}
		replay = append(replay, seg)
		expect = seg.Last
	}
	if !hasCP {
		cp = Checkpoint{}
	}
	return m.load(cp, replay)
}
