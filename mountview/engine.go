package mountview

import "fmt"

const maxSymlinkHops = 40

// groupCell is the may-lattice state of one hardlink group. Exact* fields hold
// the last authoritative (concrete replay or checkpoint) value; the may* sets
// record everything a missing window could additionally produce.
type groupCell struct {
	id          GroupID
	present     bool
	mayAbsent   bool
	exactObject ObjectID
	mayObjects  *setO
	exactInter  Interval
	mayInters   setI
	exactPerm   Perm
	mayPerms    setP
	exactProp   Propagation
	mayProps    map[Propagation]bool
	exactDomain DomainID
	mayDomains  map[DomainID]bool
	newGroup    bool // minted inside a gap (open universe)
}

// pathCell is the may-lattice state of one mount-view path.
type pathCell struct {
	path        string
	present     bool
	mayAbsent   bool
	symlink     bool
	exactTarget string
	mayTargets  setStr
	exactGroup  GroupID
	hasGroup    bool
	mayGroups   *setG
}

// Engine is the reusable, in-memory recovery verifier.
type Engine struct {
	groups     map[GroupID]*groupCell
	paths      map[string]*pathCell
	lastSeq    uint64
	lastCkpt   *Checkpoint
	gapCount   int
	notes      []string
	verifier   Verifier
	maxNewGID  GroupID
	lastReplay *Input
}

// NewEngine constructs an empty engine.
func NewEngine() *Engine {
	return &Engine{
		groups:   map[GroupID]*groupCell{},
		paths:    map[string]*pathCell{},
		verifier: NopVerifier(),
	}
}

func (e *Engine) note(format string, args ...any) {
	e.notes = append(e.notes, fmt.Sprintf(format, args...))
}

// Notes returns the human-readable may-set reasoning trace collected so far.
func (e *Engine) Notes() []string {
	out := make([]string, len(e.notes))
	copy(out, e.notes)
	return out
}

func (e *Engine) group(g GroupID) *groupCell {
	c, ok := e.groups[g]
	if !ok {
		c = &groupCell{
			id:         g,
			mayObjects: newSetO(),
			mayInters:  setI{},
			mayPerms:   setP{},
			mayProps:   map[Propagation]bool{},
			mayDomains: map[DomainID]bool{},
		}
		e.groups[g] = c
	}
	return c
}

func (e *Engine) cell(p string) *pathCell {
	c, ok := e.paths[p]
	if !ok {
		c = &pathCell{path: p, mayGroups: newSetG(), mayTargets: setStr{}}
		e.paths[p] = c
	}
	if c.mayGroups == nil {
		c.mayGroups = newSetG()
	}
	if c.mayTargets == nil {
		c.mayTargets = setStr{}
	}
	return c
}

func (e *Engine) verifyManifests(ms []*Manifest) error {
	seen := map[string]*Manifest{}
	for _, m := range ms {
		if err := e.verifier.VerifyManifest(m); err != nil {
			return err
		}
		cp, err := CleanPath(m.Path)
		if err != nil {
			return err
		}
		if prev, ok := seen[cp]; ok {
			if !equalBytes(prev.Digest, m.ParentDigest) && !equalBytes(prev.Digest, m.Digest) {
				return fmt.Errorf("mountview: conflicting manifests at %s", cp)
			}
		}
		seen[cp] = m
	}
	for _, m := range ms {
		cp, _ := CleanPath(m.Path)
		parent := parentPath(cp)
		if parent == "" {
			if len(m.ParentDigest) != 0 {
				return fmt.Errorf("mountview: root manifest %s must not carry a parent digest", cp)
			}
			continue
		}
		pm, ok := seen[parent]
		if !ok {
			return fmt.Errorf("mountview: manifest %s references unknown parent %s", cp, parent)
		}
		if !equalBytes(pm.Digest, m.ParentDigest) {
			return fmt.Errorf("mountview: manifest parent digest broken at %s", cp)
		}
	}
	return nil
}
