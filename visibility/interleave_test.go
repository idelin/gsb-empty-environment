package visibility

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 独立预言机：不使用 Index 的内部状态，仅凭自己记录的操作日志重放查询，
// 与被测实现交叉核对。事件带“发生时的索引版本”，代次由预言机按文档独立计数。
type oracle struct {
	events []oracleEvent
	auth   map[int64]map[DocID]struct{} // 快照 ID -> 发放时刻的授权集合
	gens   map[DocID]Gen                // 每个文档的已分配代次
}

type oracleEvent struct {
	version Version
	kind    kind
	doc     DocID
	gen     Gen
	terms   []string
}

type oracleRoot struct{ version Version }

type oracleSnap struct{ id int64 }

func newOracle() *oracle {
	return &oracle{
		auth: make(map[int64]map[DocID]struct{}),
		gens: make(map[DocID]Gen),
	}
}

func (o *oracle) put(v Version, doc DocID, terms []string) Gen {
	o.gens[doc]++
	gen := o.gens[doc]
	o.events = append(o.events, oracleEvent{
		version: v, kind: kindAdd, doc: doc, gen: gen,
		terms: normalizeTerms(terms),
	})
	return gen
}

func (o *oracle) tombstone(v Version, doc DocID, gen Gen, terms []string) {
	for _, term := range terms {
		o.events = append(o.events, oracleEvent{
			version: v, kind: kindTombstone, doc: doc, gen: gen, terms: []string{term},
		})
	}
}

func (o *oracle) snapshot(id int64, current map[DocID]struct{}) {
	frozen := make(map[DocID]struct{}, len(current))
	for doc := range current {
		frozen[doc] = struct{}{}
	}
	o.auth[id] = frozen
}

func (o *oracle) releaseSnapshot(id int64) { delete(o.auth, id) }

// query 重放固定根版本之前的所有事件，计算预期的当前代存活文档。
func (o *oracle) query(r oracleRoot, s oracleSnap, terms []string) []DocResult {
	if len(terms) == 0 {
		return nil
	}
	want := normalizeTerms(terms)
	alive := make(map[DocID]Gen)
	// 每个存活文档当前代的词项集合。
	docTerms := make(map[DocID]map[string]struct{})

	for _, ev := range o.events {
		if ev.version > r.version {
			break
		}
		switch ev.kind {
		case kindAdd:
			alive[ev.doc] = ev.gen
			set := make(map[string]struct{}, len(ev.terms))
			for _, term := range ev.terms {
				set[term] = struct{}{}
			}
			docTerms[ev.doc] = set
		case kindTombstone:
			if g, ok := alive[ev.doc]; ok && g == ev.gen {
				delete(alive, ev.doc)
				delete(docTerms, ev.doc)
			}
		}
	}

	allowed := o.auth[s.id]
	var out []DocResult
	for doc, gen := range alive {
		set := docTerms[doc]
		coversAll := true
		for _, term := range want {
			if _, ok := set[term]; !ok {
				coversAll = false
				break
			}
		}
		if !coversAll {
			continue
		}
		if _, ok := allowed[doc]; !ok {
			continue
		}
		out = append(out, DocResult{Doc: doc, Gen: gen})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Doc < out[j].Doc })
	return out
}

func sameResults(a, b []DocResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 随机交错删除重建、授权撤回与压缩，每一步都对全部已持有根 × 快照 ×
// 多组词项做预言机核对。
func TestInterleavedAgainstOracle(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 99} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			ix := New()
			az := NewAuthz()
			orc := newOracle()

			docs := []DocID{"d0", "d1", "d2", "d3", "d4"}
			termsPool := []string{"a", "b", "c", "d", "e"}
			type liveInfo struct {
				gen   Gen
				terms []string
			}
			live := make(map[DocID]liveInfo)
			authCurrent := make(map[DocID]struct{})

			type heldRoot struct {
				root *Root
				ora  oracleRoot
			}
			type heldSnap struct {
				snap *Snapshot
				ora  oracleSnap
			}
			var roots []heldRoot
			var snaps []heldSnap
			snapSeq := int64(0)

			roots = append(roots, heldRoot{ix.Head(), oracleRoot{version: 0}})
			orc.snapshot(snapSeq, authCurrent)
			snaps = append(snaps, heldSnap{az.Snapshot(), oracleSnap{id: snapSeq}})
			snapSeq++

			pickTerms := func() []string {
				n := 1 + rng.Intn(3)
				picked := make([]string, 0, n)
				for i := 0; i < n; i++ {
					picked = append(picked, termsPool[rng.Intn(len(termsPool))])
				}
				return picked
			}

			verify := func(stage string) {
				t.Helper()
				queries := [][]string{
					{"a"}, {"b"}, {"c"}, {"a", "b"}, {"c", "d"},
					{"e"}, {"z"}, {"a", "b", "c"},
				}
				for _, hr := range roots {
					for _, hs := range snaps {
						for _, q := range queries {
							got, err := ix.Query(hr.root, hs.snap, q)
							if err != nil {
								t.Fatalf("[%s] Query root v%d snap %d %v: %v",
									stage, hr.ora.version, hs.ora.id, q, err)
							}
							want := orc.query(hr.ora, hs.ora, q)
							if !sameResults(got, want) {
								t.Fatalf("[%s] mismatch root v%d snap %d q%v:\n got %v\nwant %v",
									stage, hr.ora.version, hs.ora.id, q, got, want)
							}
						}
					}
				}
			}

			// 句柄数有界，保证全组合核对的成本可控；交错覆盖由操作种类与
			// 随机释放保证。
			const (
				ops      = 600
				maxRoots = 8
				maxSnaps = 8
			)
			for step := 0; step < ops; step++ {
				doc := docs[rng.Intn(len(docs))]
				switch rng.Intn(6) {
				case 0, 1: // Put（删除后重建 / 替换都覆盖）
					picked := pickTerms()
					newRoot := ix.Put(doc, picked)
					gen := orc.put(newRoot.Version(), doc, picked)
					if info, ok := live[doc]; ok {
						orc.tombstone(newRoot.Version(), doc, info.gen, info.terms)
					}
					live[doc] = liveInfo{gen: gen, terms: normalizeTerms(picked)}
					if len(roots) < maxRoots {
						roots = append(roots, heldRoot{
							newRoot, oracleRoot{version: newRoot.Version()},
						})
					} else {
						newRoot.Release()
					}

				case 2: // Delete
					if info, ok := live[doc]; ok {
						newRoot := ix.Delete(doc)
						orc.tombstone(newRoot.Version(), doc, info.gen, info.terms)
						delete(live, doc)
						if len(roots) < maxRoots {
							roots = append(roots, heldRoot{
								newRoot, oracleRoot{version: newRoot.Version()},
							})
						} else {
							newRoot.Release()
						}
					}

				case 3: // Grant + 新快照
					az.Grant(doc)
					authCurrent[doc] = struct{}{}
					if len(snaps) < maxSnaps {
						orc.snapshot(snapSeq, authCurrent)
						snaps = append(snaps, heldSnap{az.Snapshot(), oracleSnap{id: snapSeq}})
						snapSeq++
					}

				case 4: // Revoke + 新快照
					az.Revoke(doc)
					delete(authCurrent, doc)
					if len(snaps) < maxSnaps {
						orc.snapshot(snapSeq, authCurrent)
						snaps = append(snaps, heldSnap{az.Snapshot(), oracleSnap{id: snapSeq}})
						snapSeq++
					}

				case 5: // 压缩：不产生新逻辑版本，预言机也无需事件
					ix.Compact()
				}

				// 随机释放句柄，制造不同的 rmin 边界；保留至少一个根和一个快照。
				if len(roots) > 2 && rng.Intn(3) == 0 {
					i := rng.Intn(len(roots))
					if err := roots[i].root.Release(); err != nil {
						t.Fatalf("root release: %v", err)
					}
					roots = append(roots[:i], roots[i+1:]...)
				}
				if len(snaps) > 2 && rng.Intn(3) == 0 {
					i := rng.Intn(len(snaps))
					if err := snaps[i].snap.Release(); err != nil {
						t.Fatalf("snapshot release: %v", err)
					}
					orc.releaseSnapshot(snaps[i].ora.id)
					snaps = append(snaps[:i], snaps[i+1:]...)
				}

				if step%100 == 0 {
					verify(fmt.Sprintf("step %d", step))
				}
			}

			ix.Compact()
			verify("final compact")

			for _, hr := range roots {
				if err := hr.root.Release(); err != nil {
					t.Fatal(err)
				}
			}
			for _, hs := range snaps {
				if err := hs.snap.Release(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// 直接验证压缩的结构不变量：任何版本 > rmin 的墓碑都必须存活。
func TestCompactPreservesNeededTombstones(t *testing.T) {
	ix := New()

	old := ix.Put("d1", []string{"k"})
	ix.Compact() // rmin=0，墓碑尚不存在
	del := ix.Delete("d1")

	// old(v1) 与 del(v2) 均被持有，rmin=1，v2 墓碑 > rmin，必须保留。
	ix.Compact()
	if _, ok := findPosting(ix, "k", "d1", 1, true); !ok {
		t.Fatal("needed tombstone dropped by compaction")
	}

	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	// 现在 rmin=2，所有持有根都已观察到墓碑，可回收。
	ix.Compact()
	if _, ok := findPosting(ix, "k", "d1", 1, true); ok {
		t.Fatal("tombstone not collected after all held roots observed it")
	}
	if _, ok := findPosting(ix, "k", "d1", 1, false); ok {
		t.Fatal("dead gen posting not collected after all held roots observed the deletion")
	}
	if ps := ix.index["k"]; len(ps) != 0 {
		t.Fatalf("term with no live docs should be empty after compaction, got %v", ps)
	}
	del.Release()
}
