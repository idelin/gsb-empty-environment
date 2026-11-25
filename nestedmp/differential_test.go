package nestedmp

import (
	"bytes"
	"math/rand"
	"testing"
)

func assertEquivalent(t *testing.T, res *Result, ref []RefPart, host *fakeHost, rec *recorder, want map[string][]byte, msg []byte) {
	t.Helper()
	if len(res.Parts) != len(ref) {
		t.Fatalf("part count stream=%d ref=%d", len(res.Parts), len(ref))
	}
	refByName := map[string]RefPart{}
	for _, rp := range ref {
		refByName[rp.Name] = rp
	}
	for _, sp := range res.Parts {
		rp, ok := refByName[sp.Name]
		if !ok {
			t.Fatalf("unexpected part %q", sp.Name)
		}
		if sp.BodyStart != rp.BodyStart || sp.BodyEnd != rp.BodyEnd || sp.EndOffset != rp.EndOffset ||
			sp.HeaderStart != rp.Offset || sp.Size != int64(len(rp.Body)) || sp.Digest != rp.Digest {
			t.Fatalf("part %q mismatch:\n stream=%+v\n ref(start=%d bodyEnd=%d endOff=%d size=%d digest=%x)",
				sp.Name, sp, rp.BodyStart, rp.BodyEnd, rp.EndOffset, len(rp.Body), rp.Digest)
		}
		if sp.Type == TypeAttachment {
			got, ok := host.txn.visible(sp.Name)
			if !ok {
				t.Fatalf("part %q not visible after commit", sp.Name)
			}
			if !bytes.Equal(got, want[sp.Name]) || !bytes.Equal(got, rp.Body) {
				t.Fatalf("part %q staged bytes mismatch", sp.Name)
			}
		}
	}
	if res.TotalBytes != int64(len(msg)) {
		t.Fatalf("total bytes %d want %d", res.TotalBytes, len(msg))
	}
	assertWriteSequence(t, rec, host, want)
}

func assertWriteSequence(t *testing.T, rec *recorder, host *fakeHost, want map[string][]byte) {
	t.Helper()
	var openOrder, closeOrder, digestOrder []string
	sawCommit, sawRollback := false, false
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case EvPartOpen:
			openOrder = append(openOrder, e.Name)
		case EvPartClose:
			closeOrder = append(closeOrder, e.Name)
		case EvDigestOK:
			digestOrder = append(digestOrder, e.Name)
		case EvCommit:
			sawCommit = true
		case EvRollback:
			sawRollback = true
		}
	}
	if sawRollback {
		t.Fatal("unexpected rollback on success path")
	}
	if !sawCommit || len(openOrder) == 0 ||
		len(openOrder) != len(closeOrder) || len(openOrder) != len(digestOrder) {
		t.Fatalf("callback sequence incomplete: open=%v close=%v digest=%v commit=%v",
			openOrder, closeOrder, digestOrder, sawCommit)
	}
	// 部件级偏序：每个部件在事件流中必须按 open -> digest -> close 出现。
	stage := map[string]int{}
	for _, e := range rec.snapshot() {
		var wantStage int
		switch e.Kind {
		case EvPartOpen:
			wantStage = 1
		case EvDigestOK:
			wantStage = 2
		case EvPartClose:
			wantStage = 3
		default:
			continue
		}
		if stage[e.Name] != wantStage-1 {
			t.Fatalf("part %q event %s out of order (stage=%d want=%d)",
				e.Name, e.Kind, stage[e.Name], wantStage)
		}
		stage[e.Name] = wantStage
	}
	for name, body := range want {
		sink := host.txn.sinks[name]
		var stitched bytes.Buffer
		for _, wc := range sink.writes {
			if int64(stitched.Len()) != wc.offset {
				t.Fatalf("part %q non-contiguous write offset", name)
			}
			stitched.Write(wc.data)
		}
		if !bytes.Equal(stitched.Bytes(), body) {
			t.Fatalf("part %q write callback sequence does not reconstruct body", name)
		}
	}
}

func TestDifferentialAllCutPoints(t *testing.T) {
	s := buildSample(t)
	flat := flattenRef(mustRef(t, s.msg))
	for cut := 1; cut <= len(s.msg); cut++ {
		res, host, rec, err := runStream(t, splitEvery(s.msg, cut), Options{})
		if err != nil {
			t.Fatalf("cut=%d stream failed: %v", cut, err)
		}
		assertEquivalent(t, res, flat, host, rec, s.want, s.msg)
	}
}

func TestDifferentialRandomSplits(t *testing.T) {
	s := buildSample(t)
	flat := flattenRef(mustRef(t, s.msg))
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		var chunks [][]byte
		pos := 0
		for pos < len(s.msg) {
			n := rng.Intn(9) + 1
			if pos+n > len(s.msg) {
				n = len(s.msg) - pos
			}
			chunks = append(chunks, s.msg[pos:pos+n])
			pos += n
		}
		res, host, rec, err := runStream(t, chunks, Options{Window: 1 + rng.Intn(64)})
		if err != nil {
			t.Fatalf("trial=%d failed: %v", trial, err)
		}
		assertEquivalent(t, res, flat, host, rec, s.want, s.msg)
	}
}

func mustRef(t *testing.T, msg []byte) []RefPart {
	t.Helper()
	parts, err := ReferenceParse(msg, rootB)
	if err != nil {
		t.Fatalf("reference parse: %v", err)
	}
	return parts
}
