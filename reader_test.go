package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// interleavePage 包装 MemPage。受控写入器由测试在每轮读取之间显式驱动，
// 确保“每两次读取之间内容都被改写”，而单次采样窗口内部不被打断。
type interleavePage struct {
	inner  *MemPage
	writer *Writer
	mu     sync.Mutex

	// everPublished 保存所有真实存在过的载荷（按摘要索引）。
	everPublished map[[digestSize]byte][]byte
}

func (p *interleavePage) ReadAt(b []byte, off int64) (int, error) {
	return p.inner.ReadAt(b, off)
}

func (p *interleavePage) WriteAt(b []byte, off int64) (int, error) {
	return p.inner.WriteAt(b, off)
}

func (p *interleavePage) Size() int64 { return p.inner.Size() }

// mutate 在每轮读取之间被调用：受控写入器发布一条字段布局全新的记录，
// 长度、字段与摘要同时改变。
func (p *interleavePage) mutate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	pubMutatingRecord(p)
}

var pubCounter atomic.Int64

// pubMutatingRecord 发布一条字段布局每次都不同的新记录，并登记其精确载荷。
func pubMutatingRecord(p *interleavePage) {
	id := uint64(pubCounter.Add(1))
	rec := Record{Fields: []Field{
		{ID: 1, Type: FieldUint64, Data: uint64Bytes(id)},
		{ID: 2, Type: FieldBytes, Data: []byte(fmt.Sprintf("blob-%04d-payload", id))},
		{ID: 3, Type: FieldString, Data: []byte(fmt.Sprintf("seq-record #%d", id))},
	}}
	payload, err := rec.Encoded()
	if err != nil {
		panic(err)
	}
	if _, _, err := p.writer.Publish(rec); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(payload)
	cp := make([]byte, len(payload))
	copy(cp, payload)
	p.everPublished[sum] = cp
}

func uint64Bytes(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

func newInterleavingHarness(t *testing.T, capacity uint32) (*Reader, *interleavePage) {
	t.Helper()
	page := NewMemPage(PageSize(capacity))
	writer, err := NewWriter(page, capacity)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	ip := &interleavePage{
		inner:         page,
		writer:        writer,
		everPublished: make(map[[digestSize]byte][]byte),
	}

	// 先放一条稳定记录并登记，保证页面从合法状态开始。
	pubMutatingRecord(ip)
	return NewReader(ip, ReaderConfig{MaxAttempts: 64, MaxPayload: capacity}), ip
}

// TestNeverFabricatesRecord 是核心安全属性。
//
// 受控写入器在每轮读取之间发布一条字段布局全新的记录（长度、字段、摘要
// 同时变化）。读取分两种预算运行：
//   - 小预算（2 次采样）：若两次采样恰好跨过一次改写，必然返回不确定；
//   - 正常预算：页面静止时必须成功，且结果严格等于某条真实发布过的记录。
//
// 任何被接受的记录都按摘要在“真实存在过的记录集合”中逐一核对，
// 因此长度/字段/摘要的任意拼接都不可能蒙混过关。
func TestNeverFabricatesRecord(t *testing.T) {
	const capacity = 256
	reader, ip := newInterleavingHarness(t, capacity)
	ctx := context.Background()

	var accepted, uncertain int
	for cycle := 0; cycle < 80; cycle++ {
		// 每轮读取之间，受控写入器先把页面改成一条全新记录。
		ip.mutate()

		var rec *Record
		var err error
		if cycle%2 == 0 {
			// 小预算 + 采样间改写：第一次快照读到版本 N，钩子把页面改成
			// 版本 N+1，第二次快照必然不同，两次机会用完只能返回不确定。
			tight := NewReader(ip, ReaderConfig{
				MaxAttempts:    2,
				MaxPayload:     capacity,
				BetweenSamples: ip.mutate,
			})
			rec, err = tight.Read(ctx)
		} else {
			rec, err = reader.Read(ctx)
		}
		if err == nil {
			payload, encErr := rec.Encoded()
			if encErr != nil {
				t.Fatalf("re-encode accepted record: %v", encErr)
			}
			sum := sha256.Sum256(payload)
			original, ok := ip.everPublished[sum]
			if !ok {
				t.Fatalf("fabricated record never published: %x", payload)
			}
			if string(original) != string(payload) {
				t.Fatalf("accepted payload does not match any published record byte-for-byte")
			}
			accepted++
		} else if errors.Is(err, ErrUncertain) {
			uncertain++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if accepted == 0 {
		t.Fatalf("expected some successful reads, got 0")
	}
	if uncertain == 0 {
		t.Fatalf("expected at least one uncertain result when reads cross a mutation, got 0")
	}
	t.Logf("accepted=%d uncertain=%d distinctPublished=%d", accepted, uncertain, len(ip.everPublished))
}

// TestStableReadAfterMutationsStop 写入器停止改写后，读取必须在很少尝试内稳定成功。
func TestStableReadAfterMutationsStop(t *testing.T) {
	const capacity = 256
	reader, ip := newInterleavingHarness(t, capacity)

	rec, err := reader.Read(context.Background())
	if err != nil {
		t.Fatalf("stable read failed: %v", err)
	}
	payload, err := rec.Encoded()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	sum := sha256.Sum256(payload)
	if _, ok := ip.everPublished[sum]; !ok {
		t.Fatalf("read record not among published ones")
	}
}

// TestRetryBudgetExhausted 持续奇数序号（写入窗口不结束）时，预算耗尽返回不确定。
func TestRetryBudgetExhausted(t *testing.T) {
	const capacity = 128
	page := newRawPage(PageSize(capacity))
	page.initHeader(capacity)
	reader := NewReader(page, ReaderConfig{MaxAttempts: 8, MaxPayload: capacity})

	page.setSeq(0, 1) // 写入窗口保持打开
	_, err := reader.Read(context.Background())
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("expected ErrUncertain, got %v", err)
	}
	page.setSeq(0, 0) // 回到稳定的初始状态

	// 恢复稳定后应立即能读到（初始空记录，零字段）。
	rec, err := reader.Read(context.Background())
	if err != nil {
		t.Fatalf("read after abort: %v", err)
	}
	if len(rec.Fields) != 0 {
		t.Fatalf("expected empty initial record, got %d fields", len(rec.Fields))
	}
}

// TestContextCancellation 上下文取消优先于重试。
func TestContextCancellation(t *testing.T) {
	const capacity = 128
	page := newRawPage(PageSize(capacity))
	page.initHeader(capacity)
	reader := NewReader(page, ReaderConfig{MaxAttempts: 1_000_000, MaxPayload: capacity})

	ctx, cancel := context.WithCancel(context.Background())
	page.setSeq(0, 1)
	cancel()
	_, err := reader.Read(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestDigestMismatchIsDeterministic 稳定的摘要损坏必须报确定错误，而不是不确定。
func TestDigestMismatchIsDeterministic(t *testing.T) {
	const capacity = 128
	page := newRawPage(PageSize(capacity))
	page.initHeader(capacity)
	reader := NewReader(page, ReaderConfig{MaxAttempts: 16, MaxPayload: capacity})

	payload := mustEncode(t, Record{Fields: []Field{
		{ID: 7, Type: FieldString, Data: []byte("hello")},
	}})
	page.setSeq(0, 1)
	page.putRecord(payload)
	page.mem[offDigest] ^= 0xff
	page.setSeq(0, 2)

	_, err := reader.Read(context.Background())
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
}

// TestMalformedLengthIsDeterministic 稳定复现的越界长度返回 ErrMalformed。
func TestMalformedLengthIsDeterministic(t *testing.T) {
	const capacity = 128
	page := newRawPage(PageSize(capacity))
	page.initHeader(capacity)
	reader := NewReader(page, ReaderConfig{MaxAttempts: 16, MaxPayload: capacity})

	page.setSeq(0, 1)
	binary.LittleEndian.PutUint32(page.mem[offLength:], capacity+1)
	page.setSeq(0, 2)

	_, err := reader.Read(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

// TestSequenceWraparoundWithGeneration 小天花板下序号回绕、代次递增，
// 读取器仍能结合代次正确读取，且不会把回绕前后的快照误判为一致。
func TestSequenceWraparoundWithGeneration(t *testing.T) {
	const capacity = 128
	page := NewMemPage(PageSize(capacity))
	// ceiling=4：seq 序列为 0(初始) -> 2 -> 0(回绕,gen=1) -> 2 -> 0(gen=2)。
	writer, err := newWriterWithCeiling(page, capacity, 4)
	if err != nil {
		t.Fatalf("newWriterWithCeiling: %v", err)
	}
	reader := NewReader(page, ReaderConfig{MaxAttempts: 16, MaxPayload: capacity})

	for i := 0; i < 4; i++ {
		rec := Record{Fields: []Field{{ID: 1, Type: FieldUint64, Data: uint64Bytes(uint64(i))}}}
		gen, seq, pubErr := writer.Publish(rec)
		if pubErr != nil {
			t.Fatalf("Publish %d: %v", i, pubErr)
		}
		wantSeq := uint64(2 - 2*uint64(i%2))
		wantGen := uint64((i + 1) / 2)
		if seq != wantSeq || gen != wantGen {
			t.Fatalf("publish %d: expected (gen=%d,seq=%d), got (gen=%d,seq=%d)",
				i, wantGen, wantSeq, gen, seq)
		}
		if writer.Generation() != wantGen || writer.Sequence() != wantSeq {
			t.Fatalf("publish %d: accessors report (gen=%d,seq=%d)",
				i, writer.Generation(), writer.Sequence())
		}

		got, readErr := reader.Read(context.Background())
		if readErr != nil {
			t.Fatalf("Read %d: %v", i, readErr)
		}
		if len(got.Fields) != 1 || binary.LittleEndian.Uint64(got.Fields[0].Data) != uint64(i) {
			t.Fatalf("Read %d returned wrong record: %+v", i, got.Fields)
		}
	}
}

func mustEncode(t *testing.T, rec Record) []byte {
	t.Helper()
	b, err := rec.Encoded()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}
