package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// ObjectRef identifies one derived object: a tuple in a node's output.
type ObjectRef struct {
	Node  string
	Index int
	Desc  string
}

func (o ObjectRef) String() string {
	return fmt.Sprintf("%s[%d](%s)", o.Node, o.Index, o.Desc)
}

// Affected is a derived object that retains a contribution of a deleted
// source and therefore must be recomputed.
type Affected struct {
	Object ObjectRef
	Prov   string   // canonical provenance expression
	Via    []string // deleted sources this object depends on
}

// Proof certifies that a derived object is unaffected by a deletion: it
// carries the object's exact source dependency set plus a digest of the
// canonical provenance expression, and can be verified independently.
type Proof struct {
	Object  ObjectRef
	Prov    string   // canonical provenance expression
	Sources []string // exact dependency set, sorted
	Digest  string   // sha256 of Prov
}

// Verify checks the proof's internal consistency and that its dependency
// set is disjoint from the deleted source IDs.
func (p Proof) Verify(deleted []string) bool {
	sum := sha256.Sum256([]byte(p.Prov))
	if hex.EncodeToString(sum[:]) != p.Digest {
		return false
	}
	for _, src := range p.Sources {
		// Every claimed source must literally occur in the expression.
		if !strings.Contains(p.Prov, src) {
			return false
		}
	}
	for _, d := range deleted {
		for _, src := range p.Sources {
			if src == d {
				return false
			}
		}
	}
	return true
}

// Impact is the result of analyzing a deletion request against a pipeline.
type Impact struct {
	Deleted    []string
	Recompute  []Affected // derived objects retaining deleted contributions
	Unaffected []Proof    // certificates for objects safe to keep
}

// Impact computes which derived objects of the pipeline retain a
// contribution of any deleted source ID and must be recomputed, and emits
// verifiable proofs for every object that is unaffected.
func (p *Pipeline) Impact(deleted ...string) *Impact {
	del := make(map[string]bool, len(deleted))
	for _, d := range deleted {
		del[d] = true
	}
	sorted := make([]string, len(deleted))
	copy(sorted, deleted)
	sort.Strings(sorted)
	imp := &Impact{Deleted: sorted}
	for _, n := range p.nodes {
		for i, t := range n.Output() {
			ref := ObjectRef{Node: n.Name(), Index: i, Desc: t.Desc}
			if DependsOn(t.Prov, del) {
				var via []string
				for _, src := range t.Prov.Sources() {
					if del[src] {
						via = append(via, src)
					}
				}
				imp.Recompute = append(imp.Recompute, Affected{
					Object: ref,
					Prov:   t.Prov.String(),
					Via:    via,
				})
			} else {
				imp.Unaffected = append(imp.Unaffected, Proof{
					Object:  ref,
					Prov:    t.Prov.String(),
					Sources: t.Prov.Sources(),
					Digest:  Digest(t.Prov),
				})
			}
		}
	}
	return imp
}
