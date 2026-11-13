package block1

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

func token(n byte) []byte { return []byte{n} }

func expectResult(t *testing.T, got Result, wantStatus Status, wantCode Code) {
	t.Helper()
	if got.Status != wantStatus || got.Code != wantCode {
		t.Fatalf("got status=%d code=%s err=%v, want status=%d code=%s",
			got.Status, got.Code, got.Err, wantStatus, wantCode)
	}
	if got.Status == StatusRejected && got.Err == nil {
		t.Fatal("rejected result must carry an error")
	}
}

// 大跳号：第一个包 NUM 很大且 M=1，必须直接拒绝且不创建任何状态。
func TestHighNUMNoAllocation(t *testing.T) {
	r := NewReassembler()
	f := Fragment{
		Peer:    "attacker:5683",
		Token:   token(1),
		NUM:     900000,
		More:    true,
		SZX:     SZX1024,
		Payload: bytes.Repeat([]byte{'A'}, 1024),
	}
	res := r.Process(f)
	expectResult(t, res, StatusRejected, CodeEntityIncomplete)
	if !errors.Is(res.Err, ErrOutOfOrder) {
		t.Fatalf("err = %v, want ErrOutOfOrder", res.Err)
	}
	if got := r.IncompleteCount("attacker:5683"); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0 (no state may be allocated)", got)
	}

	// 合法 NUM 0 后再跳号（缺 NUM 1，直接发 NUM 2）也必须拒绝，并给出期望号。
	first := Fragment{Peer: "attacker:5683", Token: token(2), NUM: 0, More: true, SZX: SZX16,
		Payload: bytes.Repeat([]byte{'B'}, 16)}
	if res := r.Process(first); res.Status != StatusContinue || res.WantNUM != 1 {
		t.Fatalf("first block: status=%d code=%s want continue 1", res.Status, res.Code)
	}
	jump := Fragment{Peer: "attacker:5683", Token: token(2), NUM: 2, More: true, SZX: SZX16,
		Payload: bytes.Repeat([]byte{'C'}, 16)}
	res = r.Process(jump)
	expectResult(t, res, StatusRejected, CodeEntityIncomplete)
	if res.WantNUM != 1 {
		t.Fatalf("WantNUM = %d, want 1", res.WantNUM)
	}
}

// 超限 Size1：声明超过 64 KiB 直接回 4.13；边界值 64 KiB 合法。
func TestSize1OverLimit(t *testing.T) {
	r := NewReassembler()

	tooBig := Fragment{Peer: "p:1", Token: token(1), NUM: 0, More: true, SZX: SZX1024,
		HasSize1: true, Size1: MaxBodySize + 1, Payload: make([]byte, 1024)}
	res := r.Process(tooBig)
	expectResult(t, res, StatusRejected, CodeRequestEntityTooLarge)
	if !errors.Is(res.Err, ErrSize1TooLarge) {
		t.Fatalf("err = %v, want ErrSize1TooLarge", res.Err)
	}
	if got := r.IncompleteCount("p:1"); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0", got)
	}

	// 边界：恰好 64 KiB 可以开始传输。
	edge := Fragment{Peer: "p:1", Token: token(2), NUM: 0, More: true, SZX: SZX1024,
		HasSize1: true, Size1: MaxBodySize, Payload: make([]byte, 1024)}
	if res := r.Process(edge); res.Status != StatusContinue {
		t.Fatalf("edge Size1: status=%d code=%s err=%v", res.Status, res.Code, res.Err)
	}

	// 未声明 Size1 时，实际数据同样不能越过 64 KiB 硬上限。
	hardCap := NewReassembler()
	for num := uint32(0); num < 64; num++ {
		f := Fragment{Peer: "p:2", Token: token(3), NUM: num, More: true, SZX: SZX1024,
			Payload: make([]byte, 1024)}
		if res := hardCap.Process(f); res.Status != StatusContinue {
			t.Fatalf("block %d: status=%d code=%s err=%v", num, res.Status, res.Code, res.Err)
		}
	}
	overflow := Fragment{Peer: "p:2", Token: token(3), NUM: 64, More: false, SZX: SZX1024,
		Payload: make([]byte, 1)}
	res = hardCap.Process(overflow)
	expectResult(t, res, StatusRejected, CodeRequestEntityTooLarge)
}

// 第 5 个并发传输：同一对端最多 4 个未完成传输。
func TestFifthTransferRejected(t *testing.T) {
	r := NewReassembler()
	peer := "peer:5683"

	for i := byte(1); i <= 4; i++ {
		f := Fragment{Peer: peer, Token: token(i), NUM: 0, More: true, SZX: SZX16,
			Payload: bytes.Repeat([]byte{i}, 16)}
		if res := r.Process(f); res.Status != StatusContinue {
			t.Fatalf("transfer %d: status=%d code=%s err=%v", i, res.Status, res.Code, res.Err)
		}
		if got := r.IncompleteCount(peer); got != int(i) {
			t.Fatalf("IncompleteCount = %d, want %d", got, i)
		}
	}

	fifth := Fragment{Peer: peer, Token: token(5), NUM: 0, More: true, SZX: SZX16,
		Payload: make([]byte, 16)}
	res := r.Process(fifth)
	expectResult(t, res, StatusRejected, CodeServiceUnavailable)
	if !errors.Is(res.Err, ErrTooManyTransfers) {
		t.Fatalf("err = %v, want ErrTooManyTransfers", res.Err)
	}
	if got := r.IncompleteCount(peer); got != 4 {
		t.Fatalf("IncompleteCount = %d, want 4", got)
	}

	// 其它对端不受影响。
	other := Fragment{Peer: "other:5683", Token: token(6), NUM: 0, More: true, SZX: SZX16,
		Payload: make([]byte, 16)}
	if res := r.Process(other); res.Status != StatusContinue {
		t.Fatalf("other peer blocked: code=%s err=%v", res.Code, res.Err)
	}

	// 完成一个传输后释放配额，第 5 个传输才能开始。
	final := Fragment{Peer: peer, Token: token(1), NUM: 1, More: false, SZX: SZX16,
		Payload: []byte("tail")}
	if res := r.Process(final); res.Status != StatusComplete {
		t.Fatalf("completion: status=%d code=%s err=%v", res.Status, res.Code, res.Err)
	}
	if got := r.IncompleteCount(peer); got != 3 {
		t.Fatalf("IncompleteCount = %d, want 3", got)
	}
	if res := r.Process(fifth); res.Status != StatusContinue {
		t.Fatalf("retry after slot freed: code=%s err=%v", res.Code, res.Err)
	}
}

// 中途换 ETag：整个传输被丢弃，旧 token 的后续分片不再有状态可接。
func TestETagChangeDiscardsTransfer(t *testing.T) {
	r := NewReassembler()
	peer, tok := "peer:1", token(7)

	f0 := Fragment{Peer: peer, Token: tok, NUM: 0, More: true, SZX: SZX16,
		HasETag: true, ETag: []byte("v1"), Payload: bytes.Repeat([]byte{'A'}, 16)}
	if res := r.Process(f0); res.Status != StatusContinue {
		t.Fatalf("f0: code=%s err=%v", res.Code, res.Err)
	}

	f1 := Fragment{Peer: peer, Token: tok, NUM: 1, More: false, SZX: SZX16,
		HasETag: true, ETag: []byte("v2"), Payload: []byte("x")}
	res := r.Process(f1)
	expectResult(t, res, StatusRejected, CodeConflict)
	if !errors.Is(res.Err, ErrETagChanged) {
		t.Fatalf("err = %v, want ErrETagChanged", res.Err)
	}
	if got := r.IncompleteCount(peer); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0 after discard", got)
	}

	// 状态已丢弃：旧 token 从 NUM 1 继续会因没有现存传输而收到 4.08。
	again := Fragment{Peer: peer, Token: tok, NUM: 1, More: false, SZX: SZX16,
		HasETag: true, ETag: []byte("v2"), Payload: []byte("x")}
	expectResult(t, r.Process(again), StatusRejected, CodeEntityIncomplete)
}

// 中途换 Content-Format 同样丢弃整个传输。
func TestContentFormatChangeDiscardsTransfer(t *testing.T) {
	r := NewReassembler()
	peer, tok := "peer:1", token(8)

	f0 := Fragment{Peer: peer, Token: tok, NUM: 0, More: true, SZX: SZX16,
		HasContentFormat: true, ContentFormat: 50, Payload: bytes.Repeat([]byte{'A'}, 16)}
	if res := r.Process(f0); res.Status != StatusContinue {
		t.Fatalf("f0: code=%s err=%v", res.Code, res.Err)
	}

	f1 := Fragment{Peer: peer, Token: tok, NUM: 1, More: false, SZX: SZX16,
		HasContentFormat: true, ContentFormat: 0, Payload: []byte("x")}
	res := r.Process(f1)
	expectResult(t, res, StatusRejected, CodeConflict)
	if !errors.Is(res.Err, ErrContentFormatChanged) {
		t.Fatalf("err = %v, want ErrContentFormatChanged", res.Err)
	}
	if got := r.IncompleteCount(peer); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0 after discard", got)
	}
}

// 正常路径 + SZX 全程不变 + 重组结果正确。
func TestHappyPathReassembly(t *testing.T) {
	r := NewReassembler()
	peer, tok := "peer:1", token(9)
	body := bytes.Repeat([]byte("0123456789ABCDEF"), 4) // 64 bytes, 4 blocks of 16

	for num := uint32(0); num < 3; num++ {
		f := Fragment{Peer: peer, Token: tok, NUM: num, More: true, SZX: SZX16,
			HasSize1: true, Size1: 64,
			Payload: body[num*16 : (num+1)*16]}
		res := r.Process(f)
		if res.Status != StatusContinue || res.WantNUM != num+1 {
			t.Fatalf("block %d: status=%d wantNUM=%d", num, res.Status, res.WantNUM)
		}
	}

	final := Fragment{Peer: peer, Token: tok, NUM: 3, More: false, SZX: SZX16,
		HasSize1: true, Size1: 64, Payload: body[48:]}
	res := r.Process(final)
	expectResult(t, res, StatusComplete, CodeChanged)
	if !bytes.Equal(res.Body, body) {
		t.Fatalf("reassembled body mismatch, got %d bytes", len(res.Body))
	}
	if got := r.IncompleteCount(peer); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0", got)
	}
}

// SZX 非法（含保留值 7）以及中途变化。
func TestSZXValidation(t *testing.T) {
	r := NewReassembler()
	bad := Fragment{Peer: "p:1", Token: token(1), NUM: 0, More: true, SZX: SZX(7),
		Payload: make([]byte, 16)}
	expectResult(t, r.Process(bad), StatusRejected, CodeBadRequest)

	ok := Fragment{Peer: "p:1", Token: token(2), NUM: 0, More: true, SZX: SZX128,
		Payload: make([]byte, 128)}
	if res := r.Process(ok); res.Status != StatusContinue {
		t.Fatalf("ok: code=%s err=%v", res.Code, res.Err)
	}
	changed := Fragment{Peer: "p:1", Token: token(2), NUM: 1, More: false, SZX: SZX64,
		Payload: make([]byte, 1)}
	res := r.Process(changed)
	expectResult(t, res, StatusRejected, CodeBadRequest)
	if !errors.Is(res.Err, ErrSZXChanged) {
		t.Fatalf("err = %v, want ErrSZXChanged", res.Err)
	}
	if got := r.IncompleteCount("p:1"); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0", got)
	}
}

func TestBlockOptionRoundTrip(t *testing.T) {
	v, err := EncodeBlock(0xABCDE, true, SZX512)
	if err != nil {
		t.Fatal(err)
	}
	num, more, szx := DecodeBlock(v)
	if num != 0xABCDE || !more || szx != SZX512 {
		t.Fatalf("round trip = (%x, %v, %d)", num, more, szx)
	}
	if _, err := EncodeBlock(0, false, SZX(7)); !errors.Is(err, ErrInvalidSZX) {
		t.Fatalf("err = %v, want ErrInvalidSZX", err)
	}
	if _, err := EncodeBlock(0x100000, false, SZX16); !errors.Is(err, ErrNUMTooLarge) {
		t.Fatalf("err = %v, want ErrNUMTooLarge", err)
	}
}

func TestCodeString(t *testing.T) {
	cases := map[Code]string{
		CodeContinue:              "2.31",
		CodeChanged:               "2.04",
		CodeEntityIncomplete:      "4.08",
		CodeRequestEntityTooLarge: "4.13",
		CodeServiceUnavailable:    "5.03",
		CodeConflict:              "4.09",
	}
	for code, want := range cases {
		if got := code.String(); got != want {
			t.Fatalf("Code(%d).String() = %q, want %q", code, got, want)
		}
	}
}

func TestPruneReleasesAbandonedTransfers(t *testing.T) {
	r := NewReassembler()
	f := Fragment{Peer: "p:1", Token: token(1), NUM: 0, More: true, SZX: SZX16,
		Payload: make([]byte, 16)}
	if res := r.Process(f); res.Status != StatusContinue {
		t.Fatalf("start: code=%s err=%v", res.Code, res.Err)
	}
	if removed := r.Prune(time.Millisecond); removed != 0 {
		t.Fatalf("Prune removed %d, want 0 while fresh", removed)
	}
	time.Sleep(5 * time.Millisecond)
	if removed := r.Prune(time.Millisecond); removed != 1 {
		t.Fatalf("Prune removed %d, want 1 stale transfer", removed)
	}
	if got := r.IncompleteCount("p:1"); got != 0 {
		t.Fatalf("IncompleteCount = %d, want 0 after prune", got)
	}
}

func TestConcurrentProcess(t *testing.T) {
	r := NewReassembler()
	var wg sync.WaitGroup
	for i := byte(0); i < 32; i++ {
		wg.Add(1)
		go func(i byte) {
			defer wg.Done()
			f := Fragment{Peer: "p:1", Token: token(i), NUM: 0, More: true, SZX: SZX16,
				Payload: make([]byte, 16)}
			r.Process(f)
			_ = r.IncompleteCount("p:1")
		}(i)
	}
	wg.Wait()
	if got := r.IncompleteCount("p:1"); got != MaxIncompletePerPeer {
		t.Fatalf("IncompleteCount = %d, want %d", got, MaxIncompletePerPeer)
	}
}
