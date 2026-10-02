package tagunion

import (
	"strings"
	"testing"
)

func TestBatchAtomicValidation(t *testing.T) {
	r := testRegistry(t)
	f1, err := r.Encode(tagTransfer, 2, map[string]any{"amount": uint64(9), "currency": "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := r.Encode(tagPing, 1, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	f3, err := EncodeUnknown(99, 5, []byte{0xAA, 0xBB})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := EncodeBatch(f1, f2, f3)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := r.DecodeBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 3 || vals[2].Kind != KindUnknown || vals[2].Version != 5 {
		t.Fatalf("bad batch decode: %+v", vals)
	}

	// 破坏第二个对象：整体失败，错误带分支路径与字节偏移。
	bad := append([]byte(nil), batch...)
	badOff := 1 + len(f1) + 2 // 批量计数 1 字节 + f1 + f2 的载荷区
	bad[badOff] ^= 0xFF
	got, err := r.DecodeBatch(bad)
	if err == nil {
		t.Fatal("want error for corrupted batch")
	}
	if got != nil {
		t.Fatalf("atomic decode must not return partial results, got %d values", len(got))
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error, got %T", err)
	}
	if e.Path != "obj[1]" {
		t.Fatalf("want path obj[1], got %q (err=%v)", e.Path, err)
	}
	if e.Offset < 0 || e.Offset >= len(bad) {
		t.Fatalf("offset %d out of range", e.Offset)
	}
	t.Logf("error located: %v", err)
}

func TestBatchCountValidatedBeforeAllocation(t *testing.T) {
	r := testRegistry(t)
	huge := []byte{0x80, 0x80, 0x08} // count = 2^17 > MaxBatchCount
	if _, err := r.DecodeBatch(huge); err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("want count-limit error, got %v", err)
	}
}

func TestBatchTrailingBytesRejected(t *testing.T) {
	r := testRegistry(t)
	f, err := r.Encode(tagPing, 1, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := EncodeBatch(f)
	if err != nil {
		t.Fatal(err)
	}
	batch = append(batch, 0x00)
	if _, err := r.DecodeBatch(batch); err == nil || !strings.Contains(err.Error(), "trailing bytes") {
		t.Fatalf("want trailing-bytes error, got %v", err)
	}
}

func TestFieldErrorPath(t *testing.T) {
	r := testRegistry(t)
	// 构造 profile 帧，bool 字段取值非法（0x07）。
	bad, err := EncodeUnknown(tagProfile, 1, []byte{0x03, 'a', 'd', 'a', 0x24, 0x0D, 0x07, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := EncodeBatch(bad)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.DecodeBatch(batch)
	if err == nil {
		t.Fatal("want field error")
	}
	e := err.(*Error)
	if !strings.Contains(e.Path, "obj[0]") || !strings.Contains(e.Path, "field(admin)") {
		t.Fatalf("want branch path with field, got %q (%v)", e.Path, err)
	}
	t.Logf("error located: %v", err)
}
