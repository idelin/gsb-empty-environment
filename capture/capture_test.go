package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// mutMem is a Reader over a byte buffer whose contents can change
// underneath a capture: hook runs inside every ReadAt and may rewrite
// the buffer, bumping the version counter like a racing target would.
type mutMem struct {
	mu      sync.Mutex
	buf     []byte
	version uint64
	reads   int
	hook    func(m *mutMem, addr uint64, n int)
}

func newMem(size int) *mutMem { return &mutMem{buf: make([]byte, size)} }

func (m *mutMem) ReadAt(addr uint64, buf []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hook != nil {
		m.hook(m, addr, len(buf))
	}
	if addr+uint64(len(buf)) > uint64(len(m.buf)) {
		return fmt.Errorf("unmapped [%d, %d)", addr, addr+uint64(len(buf)))
	}
	copy(buf, m.buf[addr:addr+uint64(len(buf))])
	m.reads++
	return nil
}

func (m *mutMem) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// put initializes memory without bumping the version (test setup).
func (m *mutMem) put(addr uint64, data ...byte) { copy(m.buf[addr:], data) }

func (m *mutMem) put64(addr uint64, v uint64) {
	binary.LittleEndian.PutUint64(m.buf[addr:], v)
}

// write64 mutates memory and bumps the version, like a racing thread.
// Callers hold mu if invoked from a hook.
func (m *mutMem) write64(addr uint64, v uint64) {
	binary.LittleEndian.PutUint64(m.buf[addr:], v)
	m.version++
}

func (m *mutMem) readCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reads
}

var generous = Budget{MaxBytes: 1 << 20, MaxNodes: 1 << 16, MaxDepth: 64, MaxRetries: 8}

func TestAliasingSharedCapture(t *testing.T) {
	m := newMem(256)
	// struct { a *msg; b *msg } at 0, both pointing at msg at 128.
	m.put64(0, 128)
	m.put64(8, 128)
	m.put64(128, 0xdeadbeef)

	msg := Struct{Fields: []Field{{Name: "v", Off: 0, Type: Uint64{}}}}
	root, err := Capture(m, 0, Struct{Fields: []Field{
		{Name: "a", Off: 0, Type: Ptr{Elem: msg}},
		{Name: "b", Off: 8, Type: Ptr{Elem: msg}},
	}}, generous)
	if err != nil {
		t.Fatal(err)
	}
	a, b := root.Field("a").Elem(), root.Field("b").Elem()
	if a != b {
		t.Fatalf("aliased pointers captured twice: %p vs %p", a, b)
	}
	if a.Field("v").Uint() != 0xdeadbeef {
		t.Fatalf("bad value: %#x", a.Field("v").Uint())
	}
	// Two pointer slots + one shared target = three reads, not four.
	if got := m.readCount(); got != 3 {
		t.Fatalf("target read %d times, want 3 (shared capture)", got)
	}
}

func TestCyclicPointer(t *testing.T) {
	var nodeSchema Schema
	nodeSchema = Struct{Fields: []Field{
		{Name: "val", Off: 0, Type: Uint64{}},
		{Name: "next", Off: 8, Type: Ptr{Elem: Ref{S: &nodeSchema}}},
	}}

	// Self loop at 128.
	m := newMem(256)
	m.put64(128, 7)
	m.put64(136, 128)
	root, err := Capture(m, 128, nodeSchema, generous)
	if err != nil {
		t.Fatal(err)
	}
	if root.Field("next").Elem() != root {
		t.Fatal("self-referential next did not link back to root")
	}
	if root.Field("val").Uint() != 7 {
		t.Fatal("bad val")
	}

	// Two-node cycle: A(128) <-> B(160).
	m2 := newMem(256)
	m2.put64(128, 1)
	m2.put64(136, 160)
	m2.put64(160, 2)
	m2.put64(168, 128)
	a, err := Capture(m2, 128, nodeSchema, generous)
	if err != nil {
		t.Fatal(err)
	}
	b := a.Field("next").Elem()
	if b.Field("val").Uint() != 2 {
		t.Fatal("bad B val")
	}
	if b.Field("next").Elem() != a {
		t.Fatal("cycle B->A did not link back to the shared A node")
	}
}

func TestLengthFieldRace(t *testing.T) {
	// Counted header at 512: {count, ptr=1024}; u64 elements at 1024.
	m := newMem(2048)
	m.put64(512, 3)
	m.put64(520, 1024)
	for i, v := range []uint64{10, 20, 30} {
		m.put64(1024+uint64(8*i), v)
	}

	mutated := false
	m.hook = func(m *mutMem, addr uint64, n int) {
		if mutated || addr != 512 {
			return
		}
		// Racer swaps in a longer table as the header is read.
		mutated = true
		m.write64(512, 5)
		for i, v := range []uint64{1, 2, 3, 4, 5} {
			m.write64(1024+uint64(8*i), v)
		}
	}

	root, err := Capture(m, 512, Counted{Elem: Uint64{}}, generous)
	if err != nil {
		t.Fatal(err)
	}
	if !mutated {
		t.Fatal("hook never ran")
	}
	if root.Len() != 5 {
		t.Fatalf("Len = %d, want 5 (consistent post-retry snapshot)", root.Len())
	}
	for i, v := range []uint64{1, 2, 3, 4, 5} {
		if got := root.Elems()[i].Uint(); got != v {
			t.Fatalf("elem %d = %d, want %d", i, got, v)
		}
	}
}

func TestPersistentChangeRetriesExhausted(t *testing.T) {
	m := newMem(64)
	m.put64(0, 42)
	m.hook = func(m *mutMem, addr uint64, n int) {
		m.version++ // target changes on every probe
	}
	b := generous
	b.MaxRetries = 3
	if _, err := Capture(m, 0, Uint64{}, b); !errors.Is(err, ErrRetries) {
		t.Fatalf("err = %v, want ErrRetries", err)
	}
}

func TestBudgets(t *testing.T) {
	// Node budget: a header claiming 1000 elements.
	m := newMem(1 << 20)
	m.put64(0, 1000)
	m.put64(8, 1024)
	b := generous
	b.MaxNodes = 50
	if _, err := Capture(m, 0, Counted{Elem: Uint64{}}, b); !errors.Is(err, ErrNodeBudget) {
		t.Fatalf("err = %v, want ErrNodeBudget", err)
	}

	// Byte budget: long unterminated string.
	m2 := newMem(512)
	for i := range m2.buf {
		m2.buf[i] = 'a'
	}
	b2 := generous
	b2.MaxBytes = 16
	if _, err := Capture(m2, 0, CString{Max: 256}, b2); !errors.Is(err, ErrByteBudget) {
		t.Fatalf("err = %v, want ErrByteBudget", err)
	}

	// Depth budget: chain of 8 nodes, depth limited to 3.
	m3 := newMem(512)
	var chain Schema
	chain = Struct{Fields: []Field{
		{Name: "next", Off: 0, Type: Ptr{Elem: Ref{S: &chain}}},
	}}
	for i := 0; i < 8; i++ {
		m3.put64(uint64(16*i), uint64(16*(i+1)))
	}
	b3 := generous
	b3.MaxDepth = 3
	if _, err := Capture(m3, 0, chain, b3); !errors.Is(err, ErrDepthBudget) {
		t.Fatalf("err = %v, want ErrDepthBudget", err)
	}
}

func TestImmutableSnapshot(t *testing.T) {
	m := newMem(64)
	m.put(0, 'h', 'i', 0, 'x')
	root, err := Capture(m, 0, CString{Max: 16}, generous)
	if err != nil {
		t.Fatal(err)
	}
	got := root.Bytes()
	if string(got) != "hi" {
		t.Fatalf("got %q", got)
	}
	got[0] = 'X' // mutating the copy must not touch the snapshot
	if string(root.Bytes()) != "hi" {
		t.Fatal("snapshot mutated through accessor")
	}
}

func TestNullPointer(t *testing.T) {
	m := newMem(64)
	root, err := Capture(m, 0, Ptr{Elem: Uint64{}}, generous)
	if err != nil {
		t.Fatal(err)
	}
	if root.Elem() != nil {
		t.Fatal("null pointer should capture as nil Elem")
	}
}

func TestVersionStableNoRetry(t *testing.T) {
	m := newMem(64)
	m.put64(0, 99)
	root, err := Capture(m, 0, Uint64{}, Budget{MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	if root.Uint() != 99 {
		t.Fatal("bad value")
	}
	if got := m.readCount(); got != 1 {
		t.Fatalf("reads = %d, want 1 (no spurious retry)", got)
	}
}
