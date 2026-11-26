package mountview

import "testing"

type fixture struct {
	t         *testing.T
	signer    *Ed25519KeyPair
	seq       uint64
	lastCk    []byte
	manifests map[string]*Manifest
	openGap   *GapSpec
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := NewSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return &fixture{t: t, signer: s, manifests: map[string]*Manifest{}}
}

func (f *fixture) manifest(parentPath, path string, g GroupID, obj ObjectID, iv Interval, pm Perm, prop Propagation, dom DomainID) *Manifest {
	var parentDigest []byte
	if parentPath != "" {
		pm2, ok := f.manifests[parentPath]
		if !ok {
			f.t.Fatalf("missing parent manifest %s", parentPath)
		}
		parentDigest = pm2.Digest
	}
	m := &Manifest{
		Path: path, ParentDigest: parentDigest, Group: g, Object: obj,
		Interval: iv, Perm: pm, Propagation: prop, Domain: dom,
	}
	if err := f.signer.SignManifest(m); err != nil {
		f.t.Fatalf("sign manifest: %v", err)
	}
	f.manifests[path] = m
	return m
}

func (f *fixture) entry(op OpType, mut func(*LogEntry)) *LogEntry {
	f.seq++
	e := &LogEntry{Seq: f.seq, Op: op}
	if mut != nil {
		mut(e)
	}
	if err := f.signer.SignEntry(e); err != nil {
		f.t.Fatalf("sign entry: %v", err)
	}
	return e
}

func (f *fixture) checkpoint(view []ViewBinding, links []SymlinkState, gaps []GapSpec) *Checkpoint {
	f.seq++
	c := &Checkpoint{
		Seq: f.seq, View: view, Links: links, Gaps: gaps,
		ParentDigest: f.lastCk,
	}
	if err := f.signer.SignCheckpoint(c); err != nil {
		f.t.Fatalf("sign checkpoint: %v", err)
	}
	f.lastCk = c.Digest
	f.openGap = nil
	return c
}

// openNextGap records an unclosed trailing window for log-end scenarios.
func (f *fixture) openNextGap(allowed map[OpType]bool, c GapCandidates) GapSpec {
	g := f.nextGap(allowed, c)
	f.openGap = &g
	return g
}

func (f *fixture) openGaps() []GapSpec {
	if f.openGap == nil {
		return nil
	}
	return []GapSpec{*f.openGap}
}

func binding(path string, g GroupID, obj ObjectID, iv Interval, pm Perm, prop Propagation, dom DomainID) ViewBinding {
	return ViewBinding{Path: path, Group: g, Object: obj, Interval: iv, Perm: pm, Propagation: prop, Domain: dom}
}

func ro() Perm  { return Perm{Read: true} }
func rw() Perm  { return Perm{Read: true, Write: true} }
func rwx() Perm { return Perm{Read: true, Write: true, Execute: true} }

// nextGap builds a gap that the following checkpoint (created by checkpoint)
// will close: fixture checkpoints increment seq, so BeforeSeq = seq+1.
func (f *fixture) nextGap(allowed map[OpType]bool, c GapCandidates) GapSpec {
	if c.MaxOps < 2 {
		c.MaxOps = 2
	}
	return GapSpec{AfterSeq: f.seq, BeforeSeq: f.seq + 1, Allowed: allowed, Candidates: c}
}

func manifestsOf(f *fixture) []*Manifest {
	out := make([]*Manifest, 0, len(f.manifests))
	for _, m := range f.manifests {
		out = append(out, m)
	}
	return out
}
