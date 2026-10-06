package prefixdict

import (
	"reflect"
	"testing"
)

func assertTopK(t *testing.T, got []WordScore, want ...WordScore) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("top-k mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func ws(word string, score int64) WordScore { return WordScore{Word: word, Score: score} }

func TestAggregatesAcrossSources(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"apple": 3, "apex": 1})
	d.ReplaceSource("b", map[string]int64{"apple": 4, "apricot": 9})
	assertTopK(t, d.TopK("ap", 3), ws("apricot", 9), ws("apple", 7), ws("apex", 1))
}

func TestDuplicateSourceUpdatesReplaceNotAccumulate(t *testing.T) {
	d := New()
	d.ReplaceSource("s", map[string]int64{"apple": 5, "ant": 2})
	d.ReplaceSource("s", map[string]int64{"apple": 1})           // ant revoked, apple overwritten
	d.ReplaceSource("s", map[string]int64{"apple": 1, "ant": 7}) // same source again
	assertTopK(t, d.TopK("a", 5), ws("ant", 7), ws("apple", 1))
	if sc, ok := d.Score("apple"); !ok || sc != 1 {
		t.Fatalf("apple score = %d, %v; want 1, true", sc, ok)
	}
}

func TestRemoveSourceRevokesOnlyItsContribution(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"x": 5, "y": 1})
	d.ReplaceSource("b", map[string]int64{"x": 2})
	d.RemoveSource("a")
	assertTopK(t, d.TopK("", 5), ws("x", 2))
	if _, ok := d.Score("y"); ok {
		t.Fatal("y must disappear once its only source is removed")
	}
}

func TestZeroWeightWordExistsUntilLastSourceRemoved(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"w": 0, "v": 1})
	assertTopK(t, d.TopK("", 2), ws("v", 1), ws("w", 0)) // zero-weight word still listed

	d.ReplaceSource("b", map[string]int64{"w": 0})
	d.RemoveSource("a")
	assertTopK(t, d.TopK("", 2), ws("w", 0)) // still alive via source b

	d.RemoveSource("b")
	assertTopK(t, d.TopK("", 2)) // last source gone: word disappears
}

func TestNegativeAndZeroWeightsOrder(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"p": -2, "q": 0, "r": -1})
	assertTopK(t, d.TopK("", 3), ws("q", 0), ws("r", -1), ws("p", -2))
}

func TestTiesBreakByUTF8ByteOrder(t *testing.T) {
	d := New()
	// 'z' (0x7A) sorts before 0xC3 (first byte of "é"); shorter prefix sorts first.
	d.ReplaceSource("s", map[string]int64{
		"apple":  5,
		"apples": 5,
		"zeta":   5,
		"élan":   5,
	})
	assertTopK(t, d.TopK("", 4), ws("apple", 5), ws("apples", 5), ws("zeta", 5), ws("élan", 5))
}

func TestUnicodePrefixes(t *testing.T) {
	d := New()
	d.ReplaceSource("s", map[string]int64{
		"你好":     2,
		"你好吗":    3,
		"他好":     9,
		"héllo":  1,
		"hélium": 4,
		"🙂":      1,
		"🙂🙃":     2,
	})
	assertTopK(t, d.TopK("你", 5), ws("你好吗", 3), ws("你好", 2))
	assertTopK(t, d.TopK("你好", 5), ws("你好吗", 3), ws("你好", 2))
	assertTopK(t, d.TopK("hél", 5), ws("hélium", 4), ws("héllo", 1))
	assertTopK(t, d.TopK("🙂", 5), ws("🙂🙃", 2), ws("🙂", 1))
	assertTopK(t, d.TopK("🙃", 5))
}

func TestTopKEdgeCases(t *testing.T) {
	d := New()
	d.ReplaceSource("s", map[string]int64{"a": 1, "b": 2})
	assertTopK(t, d.TopK("", 0))
	assertTopK(t, d.TopK("", -1))
	assertTopK(t, d.TopK("zzz", 3))
	assertTopK(t, d.TopK("", 100), ws("b", 2), ws("a", 1)) // k larger than the dictionary
}

func TestSnapshotSeesOldWeights(t *testing.T) {
	d := New()
	d.ReplaceSource("s", map[string]int64{"apple": 10, "apricot": 1})
	snap := d.Snapshot()

	d.ReplaceSource("s", map[string]int64{"apple": 2, "apricot": 9})
	d.ReplaceSource("t", map[string]int64{"app": 100})

	// The old snapshot still serves the old weights and word set.
	assertTopK(t, snap.TopK("ap", 3), ws("apple", 10), ws("apricot", 1))
	// The live dictionary reflects every committed batch.
	assertTopK(t, d.TopK("ap", 3), ws("app", 100), ws("apricot", 9), ws("apple", 2))
	// A fresh snapshot matches the live view.
	assertTopK(t, d.Snapshot().TopK("ap", 3), ws("app", 100), ws("apricot", 9), ws("apple", 2))
}

func TestSnapshotSurvivesSourceRemoval(t *testing.T) {
	d := New()
	d.ReplaceSource("s", map[string]int64{"w": 4})
	snap := d.Snapshot()
	d.RemoveSource("s")
	assertTopK(t, snap.TopK("", 1), ws("w", 4))
	assertTopK(t, d.TopK("", 1))
}
