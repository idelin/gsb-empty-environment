package prefixdict

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// modelTopK is the reference full-table model: aggregate every source into
// one table, filter by prefix, sort by (score desc, word asc), take k.
// A word exists while any source lists it, even at total weight zero.
func modelTopK(sources map[string]map[string]int64, prefix string, k int) []WordScore {
	if k <= 0 {
		return nil
	}
	totals := map[string]int64{}
	for _, batch := range sources {
		for w, wt := range batch {
			totals[w] += wt // keeps the key even when the sum lands on zero
		}
	}
	out := make([]WordScore, 0, k)
	for w, total := range totals {
		if strings.HasPrefix(w, prefix) {
			out = append(out, WordScore{Word: w, Score: total})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Word < out[j].Word
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

func equalWordScores(a, b []WordScore) bool {
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

var wordPool = []string{
	"a", "ab", "abc", "apple", "apples", "apricot", "app", "apex",
	"banana", "band", "bandana", "zeta",
	"你好", "你好吗", "你", "他好",
	"héllo", "hélium", "élan", "élan vital",
	"🙂", "🙂🙃", "🙃",
}

var prefixPool = []string{
	"", "a", "ap", "app", "appl", "b", "ban", "z",
	"你", "你好", "他", "h", "hé", "hél", "é", "🙂", "🙃", "zzz",
}

// TestAgainstFullTableModel drives random batches (including repeated
// updates of the same source and source removals) and checks the live
// index, a pre-write snapshot, and the model against each other after
// every step, over ASCII and Unicode prefixes.
func TestAgainstFullTableModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	d := New()
	model := map[string]map[string]int64{}
	sources := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	randomBatch := func() map[string]int64 {
		n := rng.Intn(6)
		b := make(map[string]int64, n)
		for i := 0; i < n; i++ {
			b[wordPool[rng.Intn(len(wordPool))]] = int64(rng.Intn(25) - 5) // -5..19, zeros included
		}
		return b
	}

	for step := 0; step < 400; step++ {
		snap := d.Snapshot()
		snapModel := cloneSources(model)

		src := sources[rng.Intn(len(sources))]
		switch rng.Intn(4) {
		case 0:
			if _, ok := model[src]; ok {
				d.RemoveSource(src)
				delete(model, src)
				break
			}
			fallthrough
		default:
			batch := randomBatch()
			d.ReplaceSource(src, batch)
			model[src] = batch
		}

		// Occasionally interleave a background rebuild with the writes.
		if rng.Intn(6) == 0 {
			r := d.StartRebuild()
			src2 := sources[rng.Intn(len(sources))]
			batch := randomBatch()
			d.ReplaceSource(src2, batch)
			model[src2] = batch
			r.Finish()
		}

		for _, prefix := range prefixPool {
			k := rng.Intn(7)
			want := modelTopK(model, prefix, k)
			if got := d.TopK(prefix, k); !equalWordScores(got, want) {
				t.Fatalf("step %d: live TopK(%q, %d) = %v, model = %v", step, prefix, k, got, want)
			}
			if got := d.Snapshot().TopK(prefix, k); !equalWordScores(got, want) {
				t.Fatalf("step %d: fresh snapshot TopK(%q, %d) = %v, model = %v", step, prefix, k, got, want)
			}
			// The snapshot taken before this step's writes must still
			// answer with the old table.
			wantOld := modelTopK(snapModel, prefix, k)
			if got := snap.TopK(prefix, k); !equalWordScores(got, wantOld) {
				t.Fatalf("step %d: stale snapshot TopK(%q, %d) = %v, old model = %v", step, prefix, k, got, wantOld)
			}
		}
	}
}

// TestRandomBytePrefixes slices words at arbitrary byte offsets (splitting
// UTF-8 sequences on purpose) to confirm prefix matching stays byte-exact.
func TestRandomBytePrefixes(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	d := New()
	model := map[string]map[string]int64{}
	for i := 0; i < 60; i++ {
		src := fmt.Sprintf("s%d", rng.Intn(4))
		batch := map[string]int64{}
		for j := 0; j < 1+rng.Intn(5); j++ {
			batch[wordPool[rng.Intn(len(wordPool))]] = int64(rng.Intn(11) - 3)
		}
		d.ReplaceSource(src, batch)
		model[src] = batch
	}
	for i := 0; i < 300; i++ {
		w := wordPool[rng.Intn(len(wordPool))]
		prefix := w[:rng.Intn(len(w)+1)] // may cut a multi-byte rune in half
		k := rng.Intn(6)
		if got, want := d.TopK(prefix, k), modelTopK(model, prefix, k); !equalWordScores(got, want) {
			t.Fatalf("TopK(%q, %d) = %v, model = %v", prefix, k, got, want)
		}
	}
}

// TestRebuildStormInterleaving hammers the dictionary with writes and
// rebuilds from several goroutines, then verifies the settled state.
func TestRebuildStormInterleaving(t *testing.T) {
	d := New()
	model := map[string]map[string]int64{}

	done := make(chan struct{})
	go func() {
		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 50; i++ {
			r := d.StartRebuild()
			if rng.Intn(2) == 0 {
				r.Finish()
			} else {
				go r.Finish()
			}
		}
		close(done)
	}()

	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		src := fmt.Sprintf("s%d", rng.Intn(5))
		batch := map[string]int64{}
		for j := 0; j < 1+rng.Intn(4); j++ {
			batch[wordPool[rng.Intn(len(wordPool))]] = int64(rng.Intn(20) - 4)
		}
		d.ReplaceSource(src, batch)
		model[src] = batch
		if rng.Intn(10) == 0 {
			victim := fmt.Sprintf("s%d", rng.Intn(5))
			d.RemoveSource(victim)
			delete(model, victim)
		}
	}
	<-done
	d.RebuildIndex() // settle every in-flight rebuild's effects

	for _, prefix := range prefixPool {
		for _, k := range []int{1, 3, 8} {
			if got, want := d.TopK(prefix, k), modelTopK(model, prefix, k); !equalWordScores(got, want) {
				t.Fatalf("TopK(%q, %d) = %v, model = %v", prefix, k, got, want)
			}
		}
	}
}

func cloneSources(m map[string]map[string]int64) map[string]map[string]int64 {
	out := make(map[string]map[string]int64, len(m))
	for s, b := range m {
		nb := make(map[string]int64, len(b))
		for w, wt := range b {
			nb[w] = wt
		}
		out[s] = nb
	}
	return out
}
