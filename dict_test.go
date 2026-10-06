package prefixdict

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func checkTopK(t *testing.T, got, want []Entry, ctx string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got  %v\n want %v", ctx, got, want)
	}
}

func TestDuplicateSourceUpdatesReplaceNotAccumulate(t *testing.T) {
	db := NewDB()
	m := newModel()
	rng := rand.New(rand.NewSource(1))
	sources := []string{"s1", "s2", "s3"}
	words := []string{"apple", "app", "apply", "应用", "apé"}
	for i := 0; i < 500; i++ {
		source := sources[rng.Intn(len(sources))]
		word := words[rng.Intn(len(words))]
		weight := int64(rng.Intn(11) - 3) // includes negatives and zero
		db.Set(source, word, weight)
		m.set(source, word, weight)
	}
	for _, prefix := range []string{"", "a", "ap", "app", "应", "apé"} {
		for _, k := range []int{1, 3, 10} {
			checkTopK(t, db.TopK(prefix, k), m.topK(prefix, k),
				fmt.Sprintf("prefix=%q k=%d", prefix, k))
		}
	}
}

func TestZeroWeightWordPersistsUntilLastSourceRemoved(t *testing.T) {
	db := NewDB()
	db.Set("a", "w", 0)
	checkTopK(t, db.TopK("", 10), []Entry{{Word: "w", Weight: 0}}, "zero weight visible")

	db.Set("b", "w", 5)
	db.Set("a", "w", 0) // duplicate update, still zero
	checkTopK(t, db.TopK("", 10), []Entry{{Word: "w", Weight: 5}}, "other source contributes")

	db.RemoveSource("b")
	checkTopK(t, db.TopK("", 10), []Entry{{Word: "w", Weight: 0}}, "back to zero, still exists")

	db.RemoveSource("a")
	checkTopK(t, db.TopK("", 10), nil, "last source removed, word gone")
}

func TestRemoveSourceOnlyWithdrawsOwnContribution(t *testing.T) {
	db := NewDB()
	db.Set("x", "w", 7)
	db.Set("y", "w", 3)
	db.Set("x", "v", 1)
	db.RemoveSource("x")
	checkTopK(t, db.TopK("", 10), []Entry{{Word: "w", Weight: 3}}, "only x's share withdrawn")
}

func TestUnicodePrefixAndByteOrderTies(t *testing.T) {
	db := NewDB()
	m := newModel()
	// Equal totals force byte-order tie-breaks across scripts and raw bytes.
	words := []string{"应用", "应试", "app", "apple", "apé", "apz", "a\xffb", "ap", "应", ""}
	for _, w := range words {
		db.Set("s", w, 5)
		m.set("s", w, 5)
	}
	db.Set("s2", "应用", 1) // break one tie
	m.set("s2", "应用", 1)
	for _, prefix := range []string{"", "a", "ap", "app", "apé", "应", "应用", "a\xffb", "z"} {
		for _, k := range []int{1, 2, 5, 20} {
			checkTopK(t, db.TopK(prefix, k), m.topK(prefix, k),
				fmt.Sprintf("prefix=%q k=%d", prefix, k))
		}
	}
}

func TestSnapshotSeesOldWeights(t *testing.T) {
	db := NewDB()
	m := newModel()
	rng := rand.New(rand.NewSource(2))
	sources := []string{"s1", "s2"}
	words := []string{"a", "ab", "abc", "b", "汉", "汉字"}

	type pinned struct {
		snap *Snapshot
		m    *model
	}
	var snaps []pinned

	for i := 0; i < 200; i++ {
		switch rng.Intn(4) {
		case 0:
			source := sources[rng.Intn(len(sources))]
			word := words[rng.Intn(len(words))]
			weight := int64(rng.Intn(7) - 2)
			db.Set(source, word, weight)
			m.set(source, word, weight)
		case 1:
			source := sources[rng.Intn(len(sources))]
			db.RemoveSource(source)
			m.removeSource(source)
		case 2:
			source := sources[rng.Intn(len(sources))]
			batch := map[string]int64{
				words[rng.Intn(len(words))]: int64(rng.Intn(7) - 2),
				words[rng.Intn(len(words))]: int64(rng.Intn(7) - 2),
			}
			db.ReplaceSource(source, batch)
			m.replaceSource(source, batch)
		case 3:
			snaps = append(snaps, pinned{db.Snapshot(), m.clone()})
		}
	}
	// Old snapshots must still answer with the weights they captured.
	for i, p := range snaps {
		for _, prefix := range []string{"", "a", "汉"} {
			checkTopK(t, p.snap.TopK(prefix, 4), p.m.topK(prefix, 4),
				fmt.Sprintf("snapshot %d prefix=%q", i, prefix))
		}
	}
	checkTopK(t, db.TopK("", 6), m.topK("", 6), "current state")
}

func TestRebuildBackfillsInterleavedWrites(t *testing.T) {
	db := NewDB()
	m := newModel()
	rng := rand.New(rand.NewSource(3))
	sources := []string{"s1", "s2", "s3"}
	words := []string{"w0", "w1", "w2", "w3", "词", "词汇"}

	apply := func() {
		switch rng.Intn(3) {
		case 0:
			s, w := sources[rng.Intn(3)], words[rng.Intn(len(words))]
			wt := int64(rng.Intn(9) - 3)
			db.Set(s, w, wt)
			m.set(s, w, wt)
		case 1:
			s := sources[rng.Intn(3)]
			db.RemoveSource(s)
			m.removeSource(s)
		case 2:
			s := sources[rng.Intn(3)]
			batch := map[string]int64{words[rng.Intn(len(words))]: int64(rng.Intn(9) - 3)}
			db.ReplaceSource(s, batch)
			m.replaceSource(s, batch)
		}
	}

	for i := 0; i < 50; i++ {
		apply()
	}
	// Interleave background rebuilds with batches of writes; the swap must
	// backfill every write that landed after the rebuild started.
	for round := 0; round < 30; round++ {
		r := db.StartRebuild()
		for i := 0; i < 1+rng.Intn(6); i++ {
			apply()
		}
		r.Finish()
		for _, prefix := range []string{"", "w", "词"} {
			checkTopK(t, db.TopK(prefix, 4), m.topK(prefix, 4),
				fmt.Sprintf("round %d prefix=%q", round, prefix))
		}
	}
}

func TestWinnerDropPromotesRunnerUp(t *testing.T) {
	db := NewDB()
	db.Set("s", "win", 100)
	db.Set("s", "sub", 10)
	// Publish a clean index so the next write leaves a stale winner score.
	db.StartRebuild().Finish()

	snap := db.Snapshot()
	// The winner's weight collapses below the runner-up after the snapshot.
	db.ReplaceSource("s", map[string]int64{"win": 1, "sub": 10})

	checkTopK(t, snap.TopK("w", 2), []Entry{{Word: "win", Weight: 100}}, "old snapshot keeps old winner")
	checkTopK(t, db.TopK("", 2), []Entry{
		{Word: "sub", Weight: 10},
		{Word: "win", Weight: 1},
	}, "stale winner score invalidated, runner-up promoted")

	// Same, but the drop races a background rebuild: the swap must backfill it.
	db.Set("s", "win", 50)
	db.StartRebuild().Finish()
	r := db.StartRebuild()
	db.ReplaceSource("s", map[string]int64{"win": 2, "sub": 10})
	r.Finish()
	checkTopK(t, db.TopK("", 2), []Entry{
		{Word: "sub", Weight: 10},
		{Word: "win", Weight: 2},
	}, "delta backfill after rebuild swap")
}

func TestRebuildAsyncConcurrentWrites(t *testing.T) {
	db := NewDB()
	m := newModel()
	var mm sync.Mutex
	words := []string{"a0", "a1", "a2", "b0", "b1", "字"}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				word := words[r.Intn(len(words))]
				weight := int64(r.Intn(5))
				source := fmt.Sprintf("src%d", r.Intn(3))
				db.Set(source, word, weight)
				mm.Lock()
				m.set(source, word, weight)
				mm.Unlock()
			}
		}(int64(w * 1000))
	}
	for i := 0; i < 20; i++ {
		<-db.RebuildAsync()
	}
	close(stop)
	wg.Wait()
	// Quiesced: the published index must agree with the model.
	for _, prefix := range []string{"", "a", "b", "字"} {
		checkTopK(t, db.TopK(prefix, 5), m.topK(prefix, 5),
			fmt.Sprintf("prefix=%q after async rebuilds", prefix))
	}
}

// TestRandomAgainstModel is the property test: random workloads on the DB
// (including interleaved rebuilds and pinned snapshots) must always match
// full-table aggregation on the model.
func TestRandomAgainstModel(t *testing.T) {
	for _, seed := range []int64{10, 11, 12, 13, 14} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			db := NewDB()
			m := newModel()
			rng := rand.New(rand.NewSource(seed))
			sources := []string{"s1", "s2", "s3", "s4"}
			words := []string{
				"a", "ab", "abc", "abd", "b", "ba",
				"汉", "汉字", "汉语", "😀", "😀x", "é", "élan",
				"a\x00b", "z\xff",
			}
			prefixes := []string{"", "a", "ab", "b", "汉", "汉字", "😀", "é", "a\x00b", "z", "q"}

			type pinned struct {
				snap *Snapshot
				m    *model
			}
			var snaps []pinned
			var pending *Rebuild

			for step := 0; step < 800; step++ {
				switch rng.Intn(10) {
				case 0, 1, 2, 3: // Set, often repeating a source
					s := sources[rng.Intn(len(sources))]
					w := words[rng.Intn(len(words))]
					wt := int64(rng.Intn(13) - 4)
					db.Set(s, w, wt)
					m.set(s, w, wt)
				case 4: // atomic batch replace of one source
					s := sources[rng.Intn(len(sources))]
					batch := make(map[string]int64)
					for i := 0; i < rng.Intn(4); i++ {
						batch[words[rng.Intn(len(words))]] = int64(rng.Intn(13) - 4)
					}
					db.ReplaceSource(s, batch)
					m.replaceSource(s, batch)
				case 5: // remove a source
					s := sources[rng.Intn(len(sources))]
					db.RemoveSource(s)
					m.removeSource(s)
				case 6: // pin a snapshot
					snaps = append(snaps, pinned{db.Snapshot(), m.clone()})
					if len(snaps) > 4 {
						snaps = snaps[1:]
					}
				case 7: // start or finish a background rebuild
					if pending == nil {
						pending = db.StartRebuild()
					} else {
						pending.Finish()
						pending = nil
					}
				case 8, 9: // query everything
					for _, prefix := range prefixes {
						k := 1 + rng.Intn(8)
						checkTopK(t, db.TopK(prefix, k), m.topK(prefix, k),
							fmt.Sprintf("step %d prefix=%q k=%d", step, prefix, k))
						for i, p := range snaps {
							checkTopK(t, p.snap.TopK(prefix, k), p.m.topK(prefix, k),
								fmt.Sprintf("step %d snapshot %d prefix=%q k=%d", step, i, prefix, k))
						}
					}
				}
			}
			if pending != nil {
				pending.Finish()
			}
			for _, prefix := range prefixes {
				checkTopK(t, db.TopK(prefix, 20), m.topK(prefix, 20),
					fmt.Sprintf("final prefix=%q", prefix))
			}
		})
	}
}
