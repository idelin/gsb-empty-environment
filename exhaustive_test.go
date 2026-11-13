package netsec_test

import (
	"fmt"
	"sort"
	"testing"
	"time"

	netsec "example.org/netsec/q0079"
)

// exhaustiveUniverse returns every name with the zone label z and at most
// three labels total, where every non-zone label is either a or b.  With zone
// z. this is the complete set: z., a.z., b.z., a.a.z., b.a.z., a.b.z., b.b.z.
func exhaustiveUniverse() []netsec.Name {
	zone := netsec.MustParseName("z.")
	out := []netsec.Name{zone}
	layer := []netsec.Name{zone}
	for depth := 0; depth < 2; depth++ {
		next := make([]netsec.Name, 0, len(layer)*2)
		for _, parent := range layer {
			next = append(next, netsec.MustParseName("a."+parent.String()))
			next = append(next, netsec.MustParseName("b."+parent.String()))
		}
		out = append(out, next...)
		layer = next
	}
	return out
}

func existsInModel(zone netsec.Name, universe []netsec.Name, owners map[netsec.Name]bool, n netsec.Name) bool {
	if n.Compare(zone) == 0 {
		return true
	}
	for _, owner := range universe {
		if !owners[owner] {
			continue
		}
		if n.Compare(owner) == 0 || n.IsAncestorOf(owner) {
			return true
		}
	}
	return false
}

func modelClosestEncloser(zone netsec.Name, universe []netsec.Name, owners map[netsec.Name]bool, q netsec.Name) netsec.Name {
	cur, ok := q.Parent()
	if !ok {
		return zone
	}
	for cur.Compare(zone) != 0 && !existsInModel(zone, universe, owners, cur) {
		parent, ok := cur.Parent()
		if !ok {
			return zone
		}
		cur = parent
	}
	return cur
}

func TestExhaustiveThreeLabelUniverse(t *testing.T) {
	clock := newTestClock()
	zone := netsec.MustParseName("z.")
	universe := exhaustiveUniverse()
	nonZone := universe[1:]
	if len(universe) != 7 || len(nonZone) != 6 {
		t.Fatalf("universe has %d names, want 7", len(universe))
	}

	for mask := 0; mask < 1<<len(nonZone); mask++ {
		owners := map[netsec.Name]bool{zone: true}
		for i, n := range nonZone {
			if mask&(1<<i) != 0 {
				owners[n] = true
			}
		}
		ownerSlice := make([]netsec.Name, 0, len(owners))
		for _, n := range universe {
			if owners[n] {
				ownerSlice = append(ownerSlice, n)
			}
		}
		sort.Slice(ownerSlice, func(i, j int) bool {
			return ownerSlice[i].Compare(ownerSlice[j]) < 0
		})

		serial := uint32(1)
		c, err := netsec.NewCache("z.", netsec.WithClock(clock))
		if err != nil {
			t.Fatal(err)
		}
		for i, owner := range ownerSlice {
			next := ownerSlice[(i+1)%len(ownerSlice)]
			types := netsec.TypeBitmap{netsec.TypeNSEC, netsec.TypeRRSIG, netsec.TypeA}
			rec := netsec.NSECRecord{
				Owner:            owner.String(),
				Next:             next.String(),
				Types:            types,
				TTL:              3600,
				SignatureExpires: clock.Now().Add(24 * time.Hour),
				ReceiptID:        fmt.Sprintf("exhaustive-%d-%s", mask, owner),
				ZoneSerial:       serial,
			}
			receipt := netsec.Receipt{
				ID:               rec.ReceiptID,
				Zone:             "z.",
				Owner:            rec.Owner,
				Next:             rec.Next,
				Types:            types,
				SignatureExpires: rec.SignatureExpires,
				ZoneSerial:       serial,
			}
			if err := c.Add(rec, receipt); err != nil {
				t.Fatalf("mask %d add %s: %v", mask, owner, err)
			}
		}

		for _, q := range universe {
			answer, err := c.Query(q.String(), netsec.TypeTXT)
			if err != nil {
				t.Fatalf("mask %d query %s: %v", mask, q, err)
			}
			switch {
			case owners[q]:
				if answer.Status != netsec.NoData {
					t.Fatalf("mask %d owner %s: status %v, want NoData", mask, q, answer.Status)
				}
				if len(answer.Proof) != 1 || answer.Proof[0].Owner != q.String() {
					t.Fatalf("mask %d owner %s: proof %#v", mask, q, answer.Proof)
				}
			case existsInModel(zone, universe, owners, q):
				if answer.Status != netsec.NoData {
					t.Fatalf("mask %d empty non-terminal %s: status %v, want NoData", mask, q, answer.Status)
				}
				if len(answer.Proof) != 1 {
					t.Fatalf("mask %d empty non-terminal %s: proof %#v", mask, q, answer.Proof)
				}
			default:
				if answer.Status != netsec.NXDomain {
					t.Fatalf("mask %d absent %s: status %v, want NXDomain", mask, q, answer.Status)
				}
				ce := modelClosestEncloser(zone, universe, owners, q)
				wantWildcard := "*." + ce.String()
				if answer.ClosestEncloser != ce.String() || answer.Wildcard != wantWildcard {
					t.Fatalf("mask %d %s: CE/wildcard %q/%q, want %q/%q",
						mask, q, answer.ClosestEncloser, answer.Wildcard, ce, wantWildcard)
				}
				if len(answer.Proof) < 1 || len(answer.Proof) > 2 {
					t.Fatalf("mask %d absent %s: proof length %d", mask, q, len(answer.Proof))
				}
				assertCovers(t, answer.Proof, q)
				assertCovers(t, answer.Proof, netsec.MustParseName(wantWildcard))
			}

			answerA, err := c.Query(q.String(), netsec.TypeA)
			if err != nil {
				t.Fatal(err)
			}
			if owners[q] {
				if answerA.Status != netsec.NeedUpstream {
					t.Fatalf("mask %d owner %s A: status %v, want NeedUpstream", mask, q, answerA.Status)
				}
			} else if existsInModel(zone, universe, owners, q) {
				if answerA.Status != netsec.NoData {
					t.Fatalf("mask %d empty non-terminal %s A: status %v, want NoData", mask, q, answerA.Status)
				}
			} else if answerA.Status != netsec.NXDomain {
				t.Fatalf("mask %d absent %s A: status %v, want NXDomain", mask, q, answerA.Status)
			}
		}
	}
}

func assertCovers(t *testing.T, proof []netsec.NSECRecord, q netsec.Name) {
	t.Helper()
	for _, rec := range proof {
		owner := netsec.MustParseName(rec.Owner)
		next := netsec.MustParseName(rec.Next)
		if q.Compare(owner) == 0 {
			continue
		}
		if owner.Compare(next) == 0 {
			return
		}
		if owner.Compare(next) < 0 {
			if owner.Compare(q) < 0 && q.Compare(next) < 0 {
				return
			}
			continue
		}
		if owner.Compare(q) < 0 || q.Compare(next) < 0 {
			return
		}
	}
	t.Fatalf("no proof record strictly covers %s: %#v", q, proof)
}
