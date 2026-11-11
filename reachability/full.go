package reachability

import "fmt"

// Proof is a shortest-path witness that a class is anonymously public.
// It names the single complete entry path:
//
//	anonymous -> Entry -> Stmt -> [Rule] -> Class
//
// Rule is empty when the tag layer is vacuously open for the class (the
// statement has no tag rule for it), in which case Hops is 3; otherwise
// Hops is 4.
type Proof struct {
	Class ClassID
	Entry EntryID
	Stmt  StmtID
	Rule  RuleID
	Hops  int
}

func (p Proof) String() string {
	if p.Rule == "" {
		return fmt.Sprintf("anon -> %s -> %s -> %s (%d hops)", p.Entry, p.Stmt, p.Class, p.Hops)
	}
	return fmt.Sprintf("anon -> %s -> %s -> %s -> %s (%d hops)", p.Entry, p.Stmt, p.Rule, p.Class, p.Hops)
}

// less orders proofs: fewer hops first, then lexicographically by the
// path identifiers. It is the canonical tie-break shared by the full
// recompute and the incremental engine so both always agree.
func (p Proof) less(q Proof) bool {
	if p.Hops != q.Hops {
		return p.Hops < q.Hops
	}
	if p.Entry != q.Entry {
		return p.Entry < q.Entry
	}
	if p.Stmt != q.Stmt {
		return p.Stmt < q.Stmt
	}
	return p.Rule < q.Rule
}

// FullRecompute derives every publicly reachable object class from
// scratch and returns, for each, a shortest-path proof. It never mixes
// access points: a proof exists only when one entry, one of its
// statements and (if gated) one of that statement's matching tag rules
// all hold simultaneously.
func FullRecompute(m *Model) map[ClassID]Proof {
	best := make(map[ClassID]Proof, len(m.classes))
	for _, eid := range sortedEntries(m) {
		e := m.entries[eid]
		if !e.Open {
			continue
		}
		for _, sid := range m.stmtsByEntry[eid] {
			s := m.stmts[sid]
			if !s.Allows {
				continue
			}
			for _, class := range s.Grants {
				if !m.tagGated(sid, class) {
					consider(best, Proof{Class: class, Entry: eid, Stmt: sid, Hops: 3})
					continue
				}
				for _, rid := range m.rulesByStmt[sid] {
					r := m.rules[rid]
					if r.Class == class && r.Match {
						consider(best, Proof{Class: class, Entry: eid, Stmt: sid, Rule: rid, Hops: 4})
					}
				}
			}
		}
	}
	return best
}

func consider(best map[ClassID]Proof, p Proof) {
	cur, ok := best[p.Class]
	if !ok || p.less(cur) {
		best[p.Class] = p
	}
}

func sortedEntries(m *Model) []EntryID {
	out := make([]EntryID, 0, len(m.entries))
	for id := range m.entries {
		out = append(out, id)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
