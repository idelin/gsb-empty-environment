package compaction

import (
	"errors"
	"testing"
)

func TestNoSolutionSentinel(t *testing.T) {
	m := &Manifest{
		ReplacedPages: []ReplacedPage{{ID: 1, Records: []Record{rd(3, []byte("a"), []byte("z"))}}},
		// 碎片漏掉原下界键 a，且该键有更旧点值可穿透。
		OtherLayers:     []Record{pv(1, []byte("a"), []byte("old"))},
		Candidates:      []CandidatePage{mkCand(t, 2, []Record{rd(3, []byte("m"), []byte("z"))})},
		RetainIntervals: []SeqInterval{{3, 3}},
		LatestSnapshot:  3,
	}
	_, err := Solve(m)
	if !errors.Is(err, ErrNoSolution) {
		t.Fatalf("errors.Is(ErrNoSolution) failed: %v", err)
	}
	var nse *NoSolutionError
	if !errors.As(err, &nse) || len(nse.Observations) != 1 {
		t.Fatalf("want singleton NoSolutionError, got %v", err)
	}
	if msg := nse.Error(); msg == "" {
		t.Fatal("empty error message")
	}
}

func TestStructuralValidationBranches(t *testing.T) {
	good := []ReplacedPage{{ID: 1, Records: []Record{pv(2, []byte("k"), []byte("v"))}}}
	cases := []struct {
		name string
		mut  func(*Manifest)
	}{
		{"inverted interval", func(m *Manifest) { m.RetainIntervals = []SeqInterval{{9, 1}} }},
		{"dup replaced id", func(m *Manifest) {
			m.ReplacedPages = append(m.ReplacedPages, ReplacedPage{ID: 1})
		}},
		{"dup sequence", func(m *Manifest) {
			m.OtherLayers = []Record{pv(2, []byte("x"), []byte("y"))}
		}},
		{"unknown kind", func(m *Manifest) {
			m.ReplacedPages[0].Records[0].Kind = 99
		}},
		{"inverted range", func(m *Manifest) {
			m.ReplacedPages[0].Records = []Record{rd(2, []byte("z"), []byte("a"))}
		}},
		{"dup candidate id", func(m *Manifest) {
			c := mkCand(t, 5, good[0].Records)
			m.Candidates = []CandidatePage{c, c}
		}},
		{"dup dict id", func(m *Manifest) {
			m.Dictionaries = []DictSegment{mkDict(7, 1), mkDict(7, 2)}
			m.Candidates[0].DictIDs = []uint32{7}
		}},
		{"dangling dict", func(m *Manifest) {
			m.Candidates[0].DictIDs = []uint32{4242}
		}},
		{"repeated dict ref", func(m *Manifest) {
			m.Dictionaries = []DictSegment{mkDict(7, 1)}
			m.Candidates[0].DictIDs = []uint32{7, 7}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := mkCand(t, 5, good[0].Records)
			m := &Manifest{
				ReplacedPages:   good,
				Candidates:      []CandidatePage{c},
				RetainIntervals: []SeqInterval{{1, 2}},
				LatestSnapshot:  2,
			}
			tc.mut(m)
			if err := m.Validate(); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("want ErrInvalidManifest, got %v", err)
			}
		})
	}
}

func TestDecodeCorruptBranches(t *testing.T) {
	page := mkCand(t, 1, []Record{
		pv(2, []byte("k"), []byte("v")),
		rd(3, []byte("a"), []byte("z")),
	})
	if _, err := DecodePage(page.Encoding[:3]); !errors.Is(err, ErrCorruptEncoding) {
		t.Fatalf("truncated page: %v", err)
	}
	badMagic := append([]byte(nil), page.Encoding...)
	badMagic[0] = 'X'
	if _, err := DecodePage(badMagic); !errors.Is(err, ErrCorruptEncoding) {
		t.Fatalf("bad magic: %v", err)
	}
	seg := mkDict(4, 4)
	if _, err := DecodeSegment(seg.Data[:2]); !errors.Is(err, ErrCorruptEncoding) {
		t.Fatalf("truncated segment: %v", err)
	}
	badSeg := append([]byte(nil), seg.Data...)
	badSeg[len(badSeg)-1] ^= 0xff
	if _, err := DecodeSegment(append(badSeg, 0)); !errors.Is(err, ErrCorruptEncoding) {
		t.Fatalf("trailing segment bytes: %v", err)
	}
	if (Record{Kind: 99}).Covers([]byte("k")) {
		t.Fatal("unknown kind must not cover any key")
	}
}
