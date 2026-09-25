package slotstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T, opts Options) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st, dir
}

func reopen(t *testing.T, dir string, opts Options) *Store {
	t.Helper()
	st, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return st
}

func TestPutGetDelete(t *testing.T) {
	st, _ := openTemp(t, Options{SlotSize: 256, SlotCount: 8})
	defer st.Close()

	h, err := st.Put([]byte("hello"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := st.Get(h)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	if err := st.Delete(h); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Get(h); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Get after Delete: want ErrStaleHandle, got %v", err)
	}
	if err := st.Delete(h); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("double Delete: want ErrStaleHandle, got %v", err)
	}
}

func TestGenerationBumpInvalidatesOldHandle(t *testing.T) {
	st, _ := openTemp(t, Options{SlotSize: 256, SlotCount: 8})
	defer st.Close()

	h1, _ := st.Put([]byte("old-object"))
	if err := st.Delete(h1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	h2, err := st.Put([]byte("new-object"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if h2.Slot != h1.Slot {
		t.Fatalf("expected slot reuse, got %d then %d", h1.Slot, h2.Slot)
	}
	if h2.Gen != h1.Gen+1 {
		t.Fatalf("generation should increase: %d -> %d", h1.Gen, h2.Gen)
	}
	if _, err := st.Get(h1); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("old handle must be invalid, got %v", err)
	}
	got, err := st.Get(h2)
	if err != nil || string(got) != "new-object" {
		t.Fatalf("new handle broken: %q %v", got, err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	st, dir := openTemp(t, Options{SlotSize: 256, SlotCount: 8})
	h, _ := st.Put([]byte("durable"))
	st.Close()

	st2 := reopen(t, dir, Options{})
	defer st2.Close()
	got, err := st2.Get(h)
	if err != nil || string(got) != "durable" {
		t.Fatalf("after reopen: %q %v", got, err)
	}
}

func TestBadTailChecksumNotValidRecord(t *testing.T) {
	st, dir := openTemp(t, Options{SlotSize: 256, SlotCount: 8})
	h, _ := st.Put([]byte("victim-payload"))

	// 直接改数据文件：翻转 payload 区一个字节，槽尾 CRC 立即失配
	f, err := os.OpenFile(filepath.Join(dir, dataFileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	off := int64(h.Slot)*256 + slotHdrSize
	b := []byte{0}
	if _, err := f.ReadAt(b, off); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, off); err != nil {
		t.Fatal(err)
	}
	f.Sync()
	f.Close()

	if _, err := st.Get(h); !errors.Is(err, ErrCorruptSlot) {
		t.Fatalf("corrupt slot must not read as valid record, got %v", err)
	}
	st.Close()

	// 恢复：坏槽被回收，不计为有效记录；库重新变满空闲
	st2 := reopen(t, dir, Options{})
	defer st2.Close()
	if st2.FreeCount() != 8 {
		t.Fatalf("broken slot should be reclaimed, free=%d", st2.FreeCount())
	}
	if _, err := st2.Get(h); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("reclaimed slot: want ErrStaleHandle, got %v", err)
	}
}

func TestFreeListCycleDetected(t *testing.T) {
	st, dir := openTemp(t, Options{SlotSize: 256, SlotCount: 4})
	h1, _ := st.Put([]byte("a"))
	if _, err := st.Put([]byte("b")); err != nil {
		t.Fatal(err)
	}
	// 删除 h1：自由链为 0 -> 2 -> 3 -> nil
	if err := st.Delete(h1); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// 手工把槽 2 的 nextFree 改成 0，制造环 0 -> 2 -> 0 -> ...
	f, err := os.OpenFile(filepath.Join(dir, dataFileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	encodeSlot(buf, slot{state: stateFree, nextFree: 0})
	if _, err := f.WriteAt(buf, 2*256); err != nil {
		t.Fatal(err)
	}
	f.Sync()
	f.Close()

	if _, err := Open(dir, Options{}); !errors.Is(err, ErrFreeListCycle) {
		t.Fatalf("cyclic free list must fail recovery, got %v", err)
	}
}

func TestFreeListPointsToUsedSlot(t *testing.T) {
	st, dir := openTemp(t, Options{SlotSize: 256, SlotCount: 4})
	h1, _ := st.Put([]byte("x"))
	st.Close()

	// 把头文件自由链头改成指向已占用的槽 0（模拟链与槽不一致）
	hdr, ok := (&Store{dir: dir}).readHeader()
	if !ok {
		t.Fatal("read header")
	}
	hdr.freeHead = h1.Slot
	if err := os.WriteFile(filepath.Join(dir, headerFileName), encodeDiskHeader(hdr), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, Options{}); !errors.Is(err, ErrFreeListCorrupt) {
		t.Fatalf("free list pointing at used slot must fail, got %v", err)
	}
}

func TestCrashTornSlotWriteRecovered(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, Options{SlotSize: 256, SlotCount: 8})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// 注入：写槽时只写前半截（撕裂写），随后“崩溃”
	crashed := errors.New("injected crash")
	hook := func(op FaultOp, buf []byte) ([]byte, error) {
		if op == FaultSlotWrite {
			return buf[:len(buf)/2], crashed
		}
		return buf, nil
	}
	st2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	st2.hook = hook // Open 之后再武装注入点，避免干扰恢复流程自身的写
	if _, err := st2.Put([]byte("half-written")); !errors.Is(err, crashed) {
		t.Fatalf("want injected crash, got %v", err)
	}
	// 不 Close，模拟进程崩溃

	st3 := reopen(t, dir, Options{})
	defer st3.Close()
	if st3.FreeCount() != 8 {
		t.Fatalf("torn slot must be reclaimed, free=%d", st3.FreeCount())
	}
	// 恢复后库可继续正常工作
	h, err := st3.Put([]byte("after-crash"))
	if err != nil {
		t.Fatalf("Put after recovery: %v", err)
	}
	if got, _ := st3.Get(h); string(got) != "after-crash" {
		t.Fatalf("got %q", got)
	}
}

func TestCrashBetweenHeaderAndSlotWriteNoLeak(t *testing.T) {
	dir := t.TempDir()
	crashed := errors.New("injected crash")
	hook := func(op FaultOp, buf []byte) ([]byte, error) {
		if op == FaultSlotWrite { // 头文件已更新，槽尚未写：崩溃
			return buf, crashed
		}
		return buf, nil
	}
	st, err := Open(dir, Options{SlotSize: 256, SlotCount: 8})
	if err != nil {
		t.Fatal(err)
	}
	st.hook = hook
	if _, err := st.Put([]byte("doomed")); !errors.Is(err, crashed) {
		t.Fatalf("want injected crash, got %v", err)
	}
	// 模拟崩溃：不 Close 直接重开
	st2 := reopen(t, dir, Options{})
	defer st2.Close()
	if st2.FreeCount() != 8 {
		t.Fatalf("crashed Put must not leak a slot, free=%d", st2.FreeCount())
	}
}

func TestCrashDuringDeleteNoLeak(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir, Options{SlotSize: 256, SlotCount: 8})
	h, _ := st.Put([]byte("to-delete"))
	st.Close()

	// 崩溃点：Delete 已写空闲槽，头文件尚未更新
	crashed := errors.New("injected crash")
	hook := func(op FaultOp, buf []byte) ([]byte, error) {
		if op == FaultHeaderWrite {
			return buf, crashed
		}
		return buf, nil
	}
	st2, _ := Open(dir, Options{})
	st2.hook = hook
	if err := st2.Delete(h); !errors.Is(err, crashed) {
		t.Fatalf("want injected crash, got %v", err)
	}

	st3 := reopen(t, dir, Options{})
	defer st3.Close()
	if st3.FreeCount() != 8 {
		t.Fatalf("crashed Delete must not lose the freed slot, free=%d", st3.FreeCount())
	}
	if _, err := st3.Get(h); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("deleted record must stay deleted, got %v", err)
	}
}

func TestStoreFullAndReuse(t *testing.T) {
	st, _ := openTemp(t, Options{SlotSize: 256, SlotCount: 2})
	defer st.Close()
	h1, _ := st.Put([]byte("1"))
	if _, err := st.Put([]byte("2")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put([]byte("3")); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	st.Delete(h1)
	if _, err := st.Put([]byte("3")); err != nil {
		t.Fatalf("Put after Delete: %v", err)
	}
}

func TestTooLarge(t *testing.T) {
	st, _ := openTemp(t, Options{SlotSize: 64, SlotCount: 2})
	defer st.Close()
	if _, err := st.Put(bytes.Repeat([]byte("x"), 100)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if _, err := st.Put(bytes.Repeat([]byte("x"), st.Capacity())); err != nil {
		t.Fatalf("exact capacity should fit: %v", err)
	}
}
