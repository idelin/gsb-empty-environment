package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

var testKey = []byte("enterprise-secret-key")

func testKeys() KeyStore {
	return KeyStoreFunc(func(enterpriseID string) ([]byte, error) {
		if enterpriseID == "ENT-001" {
			return testKey, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrUnknownEnterprise, enterpriseID)
	})
}

// ledger 模拟银行扣款账户，记录每笔指令的扣款次数。
type ledger struct {
	mu     sync.Mutex
	debits map[string]int
	total  int64
	failOn map[string]error
}

func newLedger() *ledger {
	return &ledger{debits: make(map[string]int), failOn: make(map[string]error)}
}

func (l *ledger) debit(_ context.Context, d Detail) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err, ok := l.failOn[d.InstructionID]; ok {
		return err
	}
	l.debits[d.InstructionID]++
	l.total += d.Amount
	return nil
}

func (l *ledger) debitedTimes(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.debits[id]
}

func (l *ledger) totalDebited() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

func signedFile(t *testing.T, h Header, details []Detail) *File {
	t.Helper()
	f := &File{Header: h, Details: details}
	f.Sign(testKey)
	return f
}

func validDetails() []Detail {
	return []Detail{
		{InstructionID: "INS-1", PayerAccount: "A100", PayeeAccount: "B200", Amount: 10000},
		{InstructionID: "INS-2", PayerAccount: "A100", PayeeAccount: "B201", Amount: 20000},
		{InstructionID: "INS-3", PayerAccount: "A100", PayeeAccount: "B202", Amount: 30000},
	}
}

func validHeader() Header {
	return Header{
		EnterpriseID: "ENT-001",
		BatchNo:      "BATCH-20261113-01",
		Date:         "20261113",
		Count:        3,
		TotalAmount:  60000,
	}
}

// 合法提交：全部明细各扣款一次。
func TestProcessValidFile(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)

	res, err := p.Process(context.Background(), signedFile(t, validHeader(), validDetails()))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("status = %s, want SUCCESS", res.Status)
	}
	if res.Replayed {
		t.Fatal("first submission must not be marked replayed")
	}
	if res.Debited != 60000 || l.totalDebited() != 60000 {
		t.Fatalf("debited = %d / ledger = %d, want 60000", res.Debited, l.totalDebited())
	}
	for _, id := range []string{"INS-1", "INS-2", "INS-3"} {
		if n := l.debitedTimes(id); n != 1 {
			t.Fatalf("instruction %s debited %d times, want 1", id, n)
		}
	}
}

// 明细重复：同一指令标识重复出现，去重后只扣款一次，合计按去重后核对。
func TestProcessDuplicateDetails(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)

	details := validDetails()
	details = append(details, details[0]) // INS-1 原样重复
	h := validHeader()
	h.Count = 4 // 文件实际 4 行

	res, err := p.Process(context.Background(), signedFile(t, h, details))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("status = %s, want SUCCESS", res.Status)
	}
	if res.Duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1", res.Duplicates)
	}
	if n := l.debitedTimes("INS-1"); n != 1 {
		t.Fatalf("INS-1 debited %d times, want 1", n)
	}
	if got := l.totalDebited(); got != 60000 {
		t.Fatalf("total debited = %d, want 60000", got)
	}
}

// 明细重复但内容冲突：拒绝处理，不扣款。
func TestProcessConflictingDuplicateDetails(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)

	details := validDetails()
	conflict := details[0]
	conflict.Amount = 99999 // 同一指令标识，金额不同
	details = append(details, conflict)
	h := validHeader()
	h.Count = 4
	h.TotalAmount = 60000 + 99999

	_, err := p.Process(context.Background(), signedFile(t, h, details))
	if !errors.Is(err, ErrConflictingDetail) {
		t.Fatalf("err = %v, want ErrConflictingDetail", err)
	}
	if got := l.totalDebited(); got != 0 {
		t.Fatalf("total debited = %d, want 0", got)
	}
}

// 总额不符：头声明总额与明细合计不一致，拒绝处理，不扣款。
func TestProcessTotalMismatch(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)

	h := validHeader()
	h.TotalAmount = 60001 // 与明细合计 60000 不符

	_, err := p.Process(context.Background(), signedFile(t, h, validDetails()))
	if !errors.Is(err, ErrTotalMismatch) {
		t.Fatalf("err = %v, want ErrTotalMismatch", err)
	}
	if got := l.totalDebited(); got != 0 {
		t.Fatalf("total debited = %d, want 0", got)
	}
}

// 笔数不符：头声明笔数与明细行数不一致，拒绝处理。
func TestProcessCountMismatch(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)

	h := validHeader()
	h.Count = 2

	_, err := p.Process(context.Background(), signedFile(t, h, validDetails()))
	if !errors.Is(err, ErrCountMismatch) {
		t.Fatalf("err = %v, want ErrCountMismatch", err)
	}
	if got := l.totalDebited(); got != 0 {
		t.Fatalf("total debited = %d, want 0", got)
	}
}

// 签名无效：拒绝处理。
func TestProcessBadSignature(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)

	f := signedFile(t, validHeader(), validDetails())
	f.Signature[0] ^= 0xff

	_, err := p.Process(context.Background(), f)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	if got := l.totalDebited(); got != 0 {
		t.Fatalf("total debited = %d, want 0", got)
	}
}

// 部分失败：失败笔记录在结果中；重放同一文件返回原结果，成功笔不重复扣款。
func TestProcessPartialFailure(t *testing.T) {
	l := newLedger()
	l.failOn["INS-2"] = errors.New("payee account frozen")
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)
	ctx := context.Background()
	f := signedFile(t, validHeader(), validDetails())

	res, err := p.Process(ctx, f)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Status != StatusPartialFailure {
		t.Fatalf("status = %s, want PARTIAL_FAILURE", res.Status)
	}
	if res.Debited != 40000 {
		t.Fatalf("debited = %d, want 40000 (INS-1 + INS-3)", res.Debited)
	}
	var failed *DetailResult
	for i := range res.Details {
		if res.Details[i].InstructionID == "INS-2" {
			failed = &res.Details[i]
		}
	}
	if failed == nil || failed.OK || failed.Err == "" {
		t.Fatalf("INS-2 result = %+v, want failure with reason", failed)
	}

	// 故障恢复后重放同一文件：返回原结果，成功笔不重复扣款。
	delete(l.failOn, "INS-2")
	replayed, err := p.Process(ctx, f)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed.Replayed || replayed.Status != StatusPartialFailure {
		t.Fatalf("replay = %+v, want original PARTIAL_FAILURE result", replayed)
	}
	if n := l.debitedTimes("INS-1"); n != 1 {
		t.Fatalf("INS-1 debited %d times, want 1", n)
	}
	if n := l.debitedTimes("INS-3"); n != 1 {
		t.Fatalf("INS-3 debited %d times, want 1", n)
	}
	if got := l.totalDebited(); got != 40000 {
		t.Fatalf("total debited = %d, want 40000 (no re-debit on replay)", got)
	}
}

// 不同批次文件互不影响：摘要不同则各自处理。
func TestProcessDistinctFiles(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)
	ctx := context.Background()

	f1 := signedFile(t, validHeader(), validDetails())
	h2 := validHeader()
	h2.BatchNo = "BATCH-20261114-01"
	h2.Date = "20261114"
	f2 := signedFile(t, h2, validDetails())

	if f1.Digest() == f2.Digest() {
		t.Fatal("different batches must have different digests")
	}
	for _, f := range []*File{f1, f2} {
		if _, err := p.Process(ctx, f); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}
	if got := l.totalDebited(); got != 120000 {
		t.Fatalf("total debited = %d, want 120000", got)
	}
}

// 文件重放：同一文件重复提交（含并发），只返回原结果，扣款不重复。
func TestProcessReplayedFile(t *testing.T) {
	l := newLedger()
	p := NewProcessor(testKeys(), NewMemoryStore(), l.debit)
	f := signedFile(t, validHeader(), validDetails())
	ctx := context.Background()

	first, err := p.Process(ctx, f)
	if err != nil {
		t.Fatalf("first Process: %v", err)
	}

	// 攻击者隔天重放同一文件多次。
	for i := 0; i < 3; i++ {
		replayed, err := p.Process(ctx, f)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if !replayed.Replayed {
			t.Fatalf("replay %d not marked replayed", i)
		}
		if replayed.Digest != first.Digest || replayed.Status != first.Status ||
			replayed.Debited != first.Debited {
			t.Fatalf("replay %d returned different result", i)
		}
	}

	// 并发重放。
	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := p.Process(ctx, f)
			if err != nil {
				errs <- err
				return
			}
			if r.Debited != 60000 {
				errs <- fmt.Errorf("unexpected debited %d", r.Debited)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if got := l.totalDebited(); got != 60000 {
		t.Fatalf("total debited = %d, want 60000 (charged exactly once)", got)
	}
	for _, id := range []string{"INS-1", "INS-2", "INS-3"} {
		if n := l.debitedTimes(id); n != 1 {
			t.Fatalf("instruction %s debited %d times, want 1", id, n)
		}
	}
}
