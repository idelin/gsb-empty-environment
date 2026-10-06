package recovery_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"recovery"
)

func recs(texts ...string) []recovery.Record {
	out := make([]recovery.Record, len(texts))
	for i, t := range texts {
		out[i] = recovery.NewRecord([]byte(t))
	}
	return out
}

func corrupt(r recovery.Record) recovery.Record {
	r.Sum ^= 0xdeadbeef
	return r
}

func publishedTexts(res recovery.Result) []string {
	out := make([]string, len(res.Records))
	for i, r := range res.Records {
		out[i] = string(r.Data)
	}
	return out
}

// validLen is the number of trustworthy records at the head of s,
// i.e. everything before the first checksum failure (a torn tail).
func validLen(s recovery.Segment) uint64 {
	var n uint64
	for n < uint64(len(s.Records)) && s.Records[n].Valid() {
		n++
	}
	return n
}

func TestDeterministic(t *testing.T) {
	t.Run("single segment publishes whole run", func(t *testing.T) {
		res := recovery.Recover(10, []recovery.Segment{
			{Start: 10, Records: recs("a", "b", "c")},
		})
		want := []string{"a", "b", "c"}
		if fmt.Sprint(publishedTexts(res)) != fmt.Sprint(want) {
			t.Fatalf("got %v want %v", publishedTexts(res), want)
		}
		if fmt.Sprint(res.Sources) != "[0 0 0]" {
			t.Fatalf("sources %v", res.Sources)
		}
		if res.Conflict != nil {
			t.Fatalf("unexpected conflict %+v", res.Conflict)
		}
	})

	t.Run("overlapping agreeing segments merge", func(t *testing.T) {
		res := recovery.Recover(0, []recovery.Segment{
			{Start: 0, Records: recs("a", "b", "c")},
			{Start: 2, Records: recs("c", "d", "e")},
		})
		want := []string{"a", "b", "c", "d", "e"}
		if fmt.Sprint(publishedTexts(res)) != fmt.Sprint(want) {
			t.Fatalf("got %v want %v", publishedTexts(res), want)
		}
		if fmt.Sprint(res.Sources) != "[0 0 0 1 1]" {
			t.Fatalf("sources %v", res.Sources)
		}
	})

	t.Run("gap is not joined", func(t *testing.T) {
		res := recovery.Recover(0, []recovery.Segment{
			{Start: 0, Records: recs("a", "b")},
			{Start: 5, Records: recs("f", "g")},
		})
		if fmt.Sprint(publishedTexts(res)) != "[a b]" {
			t.Fatalf("published past gap: %v", publishedTexts(res))
		}
	})

	t.Run("conflict reports earliest seq and stops prefix", func(t *testing.T) {
		res := recovery.Recover(0, []recovery.Segment{
			{Start: 0, Records: recs("a", "b", "c", "d")},
			{Start: 1, Records: recs("b", "X", "Y")}, // conflicts at 2 and 3
		})
		if res.Conflict == nil || res.Conflict.Seq != 2 {
			t.Fatalf("conflict %+v, want earliest seq 2", res.Conflict)
		}
		if res.Conflict.Segments != [2]int{0, 1} {
			t.Fatalf("conflict segments %v", res.Conflict.Segments)
		}
		if fmt.Sprint(publishedTexts(res)) != "[a b]" {
			t.Fatalf("published past conflict: %v", publishedTexts(res))
		}
	})

	t.Run("conflict before from does not block", func(t *testing.T) {
		res := recovery.Recover(2, []recovery.Segment{
			{Start: 0, Records: recs("a", "b", "c")},
			{Start: 0, Records: recs("Z", "b", "c")}, // conflict at 0
		})
		if res.Conflict == nil || res.Conflict.Seq != 0 {
			t.Fatalf("conflict %+v, want seq 0", res.Conflict)
		}
		if fmt.Sprint(publishedTexts(res)) != "[c]" {
			t.Fatalf("got %v", publishedTexts(res))
		}
	})

	t.Run("later conflict still stops prefix when from is past the earliest", func(t *testing.T) {
		res := recovery.Recover(2, []recovery.Segment{
			{Start: 0, Records: recs("a", "b", "c", "d")},
			{Start: 0, Records: recs("Z", "b", "c", "Y")}, // conflicts at 0 and 3
		})
		if res.Conflict == nil || res.Conflict.Seq != 0 {
			t.Fatalf("conflict %+v, want earliest seq 0", res.Conflict)
		}
		if fmt.Sprint(publishedTexts(res)) != "[c]" {
			t.Fatalf("published past later conflict: %v", publishedTexts(res))
		}
	})

	t.Run("torn tail is cut at first bad checksum", func(t *testing.T) {
		torn := recs("a", "b", "c")
		torn[2] = corrupt(torn[2])
		res := recovery.Recover(0, []recovery.Segment{
			{Start: 0, Records: torn},
			{Start: 0, Records: recs("a", "b")}, // agrees with the valid head
		})
		if fmt.Sprint(publishedTexts(res)) != "[a b]" {
			t.Fatalf("got %v", publishedTexts(res))
		}
	})

	t.Run("garbage after torn record is ignored", func(t *testing.T) {
		seg := recs("a", "b")
		seg = append(seg, corrupt(recovery.NewRecord([]byte("junk"))))
		seg = append(seg, recs("x", "y")...) // valid but beyond the tear
		res := recovery.Recover(0, []recovery.Segment{{Start: 0, Records: seg}})
		if fmt.Sprint(publishedTexts(res)) != "[a b]" {
			t.Fatalf("got %v", publishedTexts(res))
		}
	})

	t.Run("conflict beyond a gap is still reported", func(t *testing.T) {
		res := recovery.Recover(0, []recovery.Segment{
			{Start: 0, Records: recs("a")},
			{Start: 4, Records: recs("e")},
			{Start: 4, Records: recs("E")},
		})
		if fmt.Sprint(publishedTexts(res)) != "[a]" {
			t.Fatalf("got %v", publishedTexts(res))
		}
		if res.Conflict == nil || res.Conflict.Seq != 4 {
			t.Fatalf("conflict %+v, want seq 4", res.Conflict)
		}
	})

	t.Run("no coverage publishes nothing", func(t *testing.T) {
		res := recovery.Recover(7, []recovery.Segment{
			{Start: 0, Records: recs("a")},
			{Start: 3, Records: nil},
		})
		if len(res.Records) != 0 || len(res.Sources) != 0 {
			t.Fatalf("got %v", publishedTexts(res))
		}
	})
}

// --- randomized checking against a per-record expanded model ---

// model is an independent reference: it expands every segment into
// per-sequence-number entries and walks the published prefix one record
// at a time. It intentionally shares no code with Recover.
func model(from uint64, segs []recovery.Segment) (texts []string, srcs []int, conflictSeq uint64, hasConflict bool) {
	type entry struct {
		data []byte
		src  int
	}
	cover := map[uint64][]entry{}
	for i, s := range segs {
		for j, r := range s.Records {
			if !r.Valid() {
				break // torn tail
			}
			seq := s.Start + uint64(j)
			cover[seq] = append(cover[seq], entry{r.Data, i})
		}
	}
	conflicted := map[uint64]bool{}
	for seq, es := range cover {
		for k := 1; k < len(es); k++ {
			if !bytes.Equal(es[0].data, es[k].data) {
				conflicted[seq] = true
				if !hasConflict || seq < conflictSeq {
					conflictSeq, hasConflict = seq, true
				}
				break
			}
		}
	}
	for seq := from; ; seq++ {
		es, ok := cover[seq]
		if !ok || conflicted[seq] {
			return
		}
		texts = append(texts, string(es[0].data))
		srcs = append(srcs, es[0].src)
	}
}

// genCase builds a ground-truth log, then random overlapping segments,
// some with torn tails, some with content conflicts.
func genCase(r *rand.Rand) (truth []recovery.Record, segs []recovery.Segment, from uint64) {
	n := r.Intn(40)
	truth = make([]recovery.Record, n)
	for i := range truth {
		truth[i] = recovery.NewRecord([]byte(fmt.Sprintf("rec-%d-%d", i, r.Intn(1000))))
	}
	for ns := r.Intn(8); ns >= 0; ns-- {
		start := uint64(r.Intn(n + 2))
		l := r.Intn(n + 2)
		end := int(start) + l
		if end > n {
			end = n
		}
		var sr []recovery.Record
		if int(start) < n {
			sr = append(sr, truth[start:end]...)
		}
		switch r.Intn(4) {
		case 0: // torn tail: drop some, corrupt the boundary record, maybe trailing garbage
			if len(sr) > 0 {
				sr = sr[:r.Intn(len(sr)+1)]
			}
			if len(sr) > 0 {
				sr[len(sr)-1] = corrupt(sr[len(sr)-1])
			}
			for g := r.Intn(3); g > 0; g-- {
				sr = append(sr, recovery.NewRecord([]byte("garbage")))
			}
		case 1: // content conflict: valid checksum, wrong data
			if len(sr) > 0 {
				sr[r.Intn(len(sr))] = recovery.NewRecord([]byte("forged"))
			}
		}
		segs = append(segs, recovery.Segment{Start: start, Records: sr})
	}
	from = uint64(r.Intn(n + 3))
	return truth, segs, from
}

func TestRandomizedAgainstModel(t *testing.T) {
	for seed := int64(0); seed < 5000; seed++ {
		r := rand.New(rand.NewSource(seed))
		_, segs, from := genCase(r)

		got := recovery.Recover(from, segs)
		wantTexts, wantSrcs, wantConflict, wantHasConflict := model(from, segs)

		if fmt.Sprint(publishedTexts(got)) != fmt.Sprint(wantTexts) {
			t.Fatalf("seed %d: records %v, model %v", seed, publishedTexts(got), wantTexts)
		}
		if fmt.Sprint(got.Sources) != fmt.Sprint(wantSrcs) {
			t.Fatalf("seed %d: sources %v, model %v", seed, got.Sources, wantSrcs)
		}
		if (got.Conflict != nil) != wantHasConflict {
			t.Fatalf("seed %d: conflict %+v, model has=%v seq=%d", seed, got.Conflict, wantHasConflict, wantConflict)
		}
		if wantHasConflict && got.Conflict.Seq != wantConflict {
			t.Fatalf("seed %d: conflict seq %d, model %d", seed, got.Conflict.Seq, wantConflict)
		}

		// Every published record must be checksum-valid and actually
		// backed by its claimed source segment at the right offset.
		for i, rec := range got.Records {
			if !rec.Valid() {
				t.Fatalf("seed %d: published invalid record at +%d", seed, i)
			}
			seq := from + uint64(i)
			src := segs[got.Sources[i]]
			off := seq - src.Start
			if off >= validLen(src) || !bytes.Equal(src.Records[off].Data, rec.Data) {
				t.Fatalf("seed %d: source mapping wrong at seq %d", seed, seq)
			}
			// Merge invariant: every segment whose trustworthy head
			// covers seq must agree with the published record.
			for j, s := range segs {
				if seq < s.Start || seq >= s.Start+validLen(s) {
					continue
				}
				other := s.Records[seq-s.Start]
				if !bytes.Equal(other.Data, rec.Data) {
					t.Fatalf("seed %d: published seq %d contradicted by segment %d", seed, seq, j)
				}
			}
		}
	}
}
