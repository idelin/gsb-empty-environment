package compaction

import "testing"

func BenchmarkSolve22(b *testing.B) {
	var replaced []Record
	var cands []CandidatePage
	for i := 0; i < maxCandidates; i++ {
		r := pv(uint64(10+i), []byte{byte('a' + i)}, []byte("v"))
		replaced = append(replaced, r)
		c := CandidatePage{ID: uint32(i + 1), Records: []Record{r}}
		c.Encoding = EncodePage(c)
		cands = append(cands, c)
	}
	m := &Manifest{
		OtherLayers:     []Record{pv(1, []byte("z"), []byte("old"))},
		ReplacedPages:   []ReplacedPage{{ID: 99, Records: replaced}},
		Candidates:      cands,
		RetainIntervals: []SeqInterval{{1, 40}},
		LatestSnapshot:  40,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Solve(m); err != nil {
			b.Fatal(err)
		}
	}
}
