package restore

import (
	"math/rand"
	"reflect"
	"testing"
)

func seg(start, end uint64, records ...[]byte) Segment {
	return Segment{Start: start, End: end, Checksum: Checksum(records), Records: records}
}

func recs(seqs []uint64, data [][]byte) []Record {
	out := make([]Record, len(seqs))
	for i := range seqs {
		out[i] = Record{Seq: seqs[i], Data: data[i]}
	}
	return out
}

func TestOverlappingConsistentSegmentsMerge(t *testing.T) {
	segs := []Segment{
		seg(0, 2, []byte("a"), []byte("b"), []byte("c")),
		seg(2, 4, []byte("c"), []byte("d"), []byte("e")), // 序号 2 重叠且一致
	}
	res, err := Restore(0, segs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := recs([]uint64{0, 1, 2, 3, 4}, [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")})
	if !reflect.DeepEqual(res.Records, want) {
		t.Fatalf("records = %+v, want %+v", res.Records, want)
	}
	if !reflect.DeepEqual(res.Sources, []int{0, 0, 0, 1, 1}) {
		t.Fatalf("sources = %v", res.Sources)
	}
	if res.Next != 5 {
		t.Fatalf("next = %d, want 5", res.Next)
	}
}

func TestTruncatedTailFilledByOverlap(t *testing.T) {
	// 段 0 声明到 5 但末尾被截断，只携带 3 条；段 1 补齐尾部。
	segs := []Segment{
		seg(0, 5, []byte("a"), []byte("b"), []byte("c")),
		seg(3, 5, []byte("d"), []byte("e"), []byte("f")),
	}
	res, err := Restore(0, segs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Records) != 6 || res.Next != 6 {
		t.Fatalf("records = %+v next = %d", res.Records, res.Next)
	}
	if !reflect.DeepEqual(res.Sources, []int{0, 0, 0, 1, 1, 1}) {
		t.Fatalf("sources = %v", res.Sources)
	}
}

func TestGapStopsPublication(t *testing.T) {
	// 序号 1 缺失：缺口之后的内容不得悄悄接上。
	segs := []Segment{
		seg(0, 0, []byte("a")),
		seg(2, 3, []byte("c"), []byte("d")),
	}
	res, err := Restore(0, segs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Records) != 1 || res.Next != 1 {
		t.Fatalf("records = %+v next = %d, want 1 record and next=1", res.Records, res.Next)
	}
}

func TestNonZeroFrom(t *testing.T) {
	segs := []Segment{seg(0, 4, []byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e"))}
	res, err := Restore(2, segs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Records) != 3 || res.Records[0].Seq != 2 || res.Next != 5 {
		t.Fatalf("records = %+v next = %d", res.Records, res.Next)
	}
}

func TestBadChecksumSegmentRejected(t *testing.T) {
	bad := seg(0, 1, []byte("a"), []byte("b"))
	bad.Checksum ^= 0xff
	segs := []Segment{
		bad,
		seg(0, 1, []byte("a"), []byte("b")), // 内容相同的完好段
	}
	res, err := Restore(0, segs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Index != 0 || res.Rejected[0].Reason != RejectBadChecksum {
		t.Fatalf("rejected = %+v", res.Rejected)
	}
	if len(res.Records) != 2 || !reflect.DeepEqual(res.Sources, []int{1, 1}) {
		t.Fatalf("records = %+v sources = %v", res.Records, res.Sources)
	}
}

func TestInvalidSegmentsRejected(t *testing.T) {
	segs := []Segment{
		{Start: 5, End: 3, Checksum: Checksum(nil)}, // Start > End
		seg(0, 0, []byte("a"), []byte("b")),         // 记录数超过声明区间
		seg(0, 1, []byte("x"), []byte("y")),
	}
	res, err := Restore(0, segs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rejected) != 2 || res.Rejected[0].Reason != RejectBadRange || res.Rejected[1].Reason != RejectTooMany {
		t.Fatalf("rejected = %+v", res.Rejected)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %+v", res.Records)
	}
}

func TestConflictReportsEarliestSeq(t *testing.T) {
	segs := []Segment{
		seg(0, 2, []byte("a"), []byte("b"), []byte("c")),
		seg(1, 3, []byte("B"), []byte("c"), []byte("d")), // 序号 1 冲突
		seg(0, 0, []byte("A")),                           // 序号 0 也冲突，但更早
	}
	_, err := Restore(0, segs)
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("err = %v, want *ConflictError", err)
	}
	if ce.Seq != 0 {
		t.Fatalf("earliest conflict = %d, want 0", ce.Seq)
	}
	if !reflect.DeepEqual(ce.Conflicts, []uint64{0, 1}) {
		t.Fatalf("conflicts = %v", ce.Conflicts)
	}
}

func TestEmptyCoverage(t *testing.T) {
	res, err := Restore(10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Records) != 0 || res.Next != 10 {
		t.Fatalf("records = %+v next = %d", res.Records, res.Next)
	}
}

// --- 逐条展开模型随机对照测试 ---

// modelEntry 是模型中单个序号的展开结果。
type modelEntry struct {
	data   []byte
	source int // 首个提供该序号的有效段下标
}

// expandModel 把段逐条展开为 序号->记录 的参考模型，
// 返回合并结果、被拒绝段数与冲突序号集合。
func expandModel(segs []Segment) (map[uint64]modelEntry, int, map[uint64]struct{}) {
	merged := make(map[uint64]modelEntry)
	conflicts := make(map[uint64]struct{})
	rejected := 0
	for i, s := range segs {
		if _, ok := validate(s); !ok {
			rejected++
			continue
		}
		for j, rec := range s.Records {
			seq := s.Start + uint64(j)
			if prev, seen := merged[seq]; seen {
				if !bytesEqual(prev.data, rec) {
					conflicts[seq] = struct{}{}
				}
				continue
			}
			merged[seq] = modelEntry{data: rec, source: i}
		}
	}
	return merged, rejected, conflicts
}

func randLog(rng *rand.Rand, n int) [][]byte {
	log := make([][]byte, n)
	for i := range log {
		b := make([]byte, 1+rng.Intn(8))
		rng.Read(b)
		log[i] = b
	}
	return log
}

// randSegment 从 log 中随机切出一段，随机截断末尾，偶尔损坏。
func randSegment(rng *rand.Rand, log [][]byte) Segment {
	n := uint64(len(log))
	start := rng.Uint64() % (n + 3)
	end := start + rng.Uint64()%(n+3-start)
	s := Segment{Start: start, End: end}
	avail := uint64(0)
	if start < n {
		avail = n - start
	}
	declared := end - start + 1
	take := declared
	if take > avail {
		take = avail
	}
	if take > 0 && rng.Intn(2) == 0 { // 随机截断末尾
		take = rng.Uint64()%(take) + uint64(rng.Intn(2))
	}
	for k := uint64(0); k < take; k++ {
		s.Records = append(s.Records, log[start+k])
	}
	s.Checksum = Checksum(s.Records)
	switch rng.Intn(20) {
	case 0: // 损坏校验和
		s.Checksum ^= 1 << uint(rng.Intn(64))
	case 1: // 篡改内容但保留旧校验和
		if len(s.Records) > 0 {
			b := append([]byte(nil), s.Records[rng.Intn(len(s.Records))]...)
			b[rng.Intn(len(b))] ^= 0x80
			s.Records[rng.Intn(len(s.Records))] = b
		}
	}
	return s
}

func TestRestoreAgainstExpansionModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < 3000; iter++ {
		log := randLog(rng, rng.Intn(30))
		nseg := rng.Intn(8)
		segs := make([]Segment, nseg)
		for i := range segs {
			segs[i] = randSegment(rng, log)
		}
		from := rng.Uint64() % (uint64(len(log)) + 3)

		res, err := Restore(from, segs)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}

		merged, rejected, conflicts := expandModel(segs)
		if len(conflicts) != 0 {
			t.Fatalf("iter %d: model produced conflicts from a single log", iter)
		}
		if len(res.Rejected) != rejected {
			t.Fatalf("iter %d: rejected = %d, model = %d", iter, len(res.Rejected), rejected)
		}

		// 期望：从 from 起模型中连续存在的最长前缀。
		var want []Record
		var wantSrc []int
		for seq := from; ; seq++ {
			e, ok := merged[seq]
			if !ok {
				break
			}
			want = append(want, Record{Seq: seq, Data: e.data})
			wantSrc = append(wantSrc, e.source)
		}
		if !reflect.DeepEqual(res.Records, want) {
			t.Fatalf("iter %d: records = %+v, model = %+v", iter, res.Records, want)
		}
		if !reflect.DeepEqual(res.Sources, wantSrc) {
			t.Fatalf("iter %d: sources = %v, model = %v", iter, res.Sources, wantSrc)
		}
		if res.Next != from+uint64(len(want)) {
			t.Fatalf("iter %d: next = %d, model = %d", iter, res.Next, from+uint64(len(want)))
		}

		// 来源映射核对：每条已发布记录确实来自所指段且内容一致。
		for k, r := range res.Records {
			src := segs[res.Sources[k]]
			off := r.Seq - src.Start
			if r.Seq < src.Start || int(off) >= len(src.Records) || !bytesEqual(src.Records[off], r.Data) {
				t.Fatalf("iter %d: bad source mapping for seq %d -> segment %d", iter, r.Seq, res.Sources[k])
			}
		}
	}
}

func TestConflictModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 3000; iter++ {
		base := randLog(rng, 1+rng.Intn(25))
		// 派生一个分叉版本，在随机位置改写内容。
		fork := make([][]byte, len(base))
		copy(fork, base)
		for i := range fork {
			if rng.Intn(4) == 0 {
				b := append([]byte(nil), fork[i]...)
				b[rng.Intn(len(b))] ^= 0x40
				fork[i] = b
			}
		}
		nseg := 1 + rng.Intn(6)
		segs := make([]Segment, nseg)
		for i := range segs {
			if rng.Intn(2) == 0 {
				segs[i] = randSegment(rng, base)
			} else {
				segs[i] = randSegment(rng, fork)
			}
		}
		from := rng.Uint64() % (uint64(len(base)) + 2)

		_, _, conflicts := expandModel(segs)
		res, err := Restore(from, segs)
		if len(conflicts) == 0 {
			if err != nil {
				t.Fatalf("iter %d: unexpected error: %v", iter, err)
			}
			continue
		}
		ce, ok := err.(*ConflictError)
		if !ok {
			t.Fatalf("iter %d: err = %v, want *ConflictError", iter, err)
		}
		// 模型中的最早冲突序号必须与报告一致。
		earliest := ^uint64(0)
		for seq := range conflicts {
			if seq < earliest {
				earliest = seq
			}
		}
		if ce.Seq != earliest {
			t.Fatalf("iter %d: earliest = %d, model = %d", iter, ce.Seq, earliest)
		}
		if len(ce.Conflicts) != len(conflicts) {
			t.Fatalf("iter %d: conflicts = %v, model has %d", iter, ce.Conflicts, len(conflicts))
		}
		for _, seq := range ce.Conflicts {
			if _, ok := conflicts[seq]; !ok {
				t.Fatalf("iter %d: reported conflict %d not in model", iter, seq)
			}
		}
		_ = res
	}
}
