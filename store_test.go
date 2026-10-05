package configstore

import (
	"bytes"
	"errors"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// memBackend is an in-memory Backend used by the fault-injection tests.
type memBackend struct {
	data []byte
}

func (m *memBackend) Read() ([]byte, error) {
	return append([]byte(nil), m.data...), nil
}

func (m *memBackend) WriteBody(image []byte) error {
	m.data = append([]byte(nil), image...)
	return nil
}

func (m *memBackend) WriteCommit() error {
	if len(m.data) != SlotSize {
		return errors.New("commit before complete body")
	}
	m.data[CommitOffset] = committedByte
	return nil
}

// committedSlot returns a valid, committed slot image.
func committedSlot(gen uint64, payload []byte) []byte {
	img := encodeSlot(gen, payload)
	img[CommitOffset] = committedByte
	return img
}

func mustPublish(t *testing.T, s *Store, cfg []byte) Meta {
	t.Helper()
	meta, err := s.Publish(cfg)
	if err != nil {
		t.Fatalf("Publish(%q): %v", cfg, err)
	}
	return meta
}

func mustLoad(t *testing.T, s *Store) ([]byte, Meta) {
	t.Helper()
	cfg, meta, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg, meta
}

func TestValidateSlotAcceptsValid(t *testing.T) {
	payload := []byte("hello config")
	img := committedSlot(7, payload)
	meta, got, err := ValidateSlot(img)
	if err != nil {
		t.Fatalf("ValidateSlot: %v", err)
	}
	if meta.Generation != 7 || meta.Length != uint32(len(payload)) {
		t.Fatalf("meta = %+v", meta)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q", got)
	}
}

// TestValidateSlotTruncationEveryByte truncates a valid committed slot at
// every possible byte offset and requires the validator to reject each
// strict prefix.
func TestValidateSlotTruncationEveryByte(t *testing.T) {
	img := committedSlot(3, []byte("truncation target"))
	if _, _, err := ValidateSlot(img); err != nil {
		t.Fatalf("full image rejected: %v", err)
	}
	for cut := 0; cut < SlotSize; cut++ {
		if _, _, err := ValidateSlot(img[:cut]); err == nil {
			t.Fatalf("truncated image of %d bytes accepted", cut)
		}
	}
}

// TestValidateSlotCorruptionEveryByte flips every byte of a valid slot and
// requires the validator to reject each single-byte corruption.
func TestValidateSlotCorruptionEveryByte(t *testing.T) {
	img := committedSlot(9, []byte("corruption target"))
	for i := 0; i < SlotSize; i++ {
		bad := append([]byte(nil), img...)
		bad[i] ^= 0xFF
		if _, _, err := ValidateSlot(bad); err == nil {
			t.Fatalf("corruption at byte %d accepted", i)
		}
	}
}

func TestValidateSlotRejectsUncommitted(t *testing.T) {
	img := encodeSlot(1, []byte("body written, flag not"))
	if _, _, err := ValidateSlot(img); err != ErrNotCommitted {
		t.Fatalf("err = %v, want ErrNotCommitted", err)
	}
}

func TestPublishLoadRoundtrip(t *testing.T) {
	s := NewStore(&memBackend{}, &memBackend{})
	if _, _, err := s.Load(); err != ErrNoConfig {
		t.Fatalf("empty Load err = %v", err)
	}
	cfgs := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	for i, cfg := range cfgs {
		meta := mustPublish(t, s, cfg)
		if meta.Generation != uint64(i+1) {
			t.Fatalf("generation = %d, want %d", meta.Generation, i+1)
		}
		got, lm := mustLoad(t, s)
		if !bytes.Equal(got, cfg) || lm.Generation != meta.Generation {
			t.Fatalf("Load = %q gen %d, want %q gen %d", got, lm.Generation, cfg, meta.Generation)
		}
	}
}

// TestCommitFlagWrittenLast records every storage operation during Publish
// and requires the commit-flag write to be the final operation, preceded by
// exactly one body write. It also checks, at the moment the body has been
// written but the flag has not, that readers still see the old value.
func TestCommitFlagWrittenLast(t *testing.T) {
	a, b := &memBackend{}, &memBackend{}
	s := NewStore(a, b)
	mustPublish(t, s, []byte("old"))

	var ops []string
	wrap := func(m *memBackend, name string, afterBody func()) Backend {
		return &logBackend{inner: m, name: name, ops: &ops, afterBody: afterBody}
	}
	bodyBeforeCommit := false
	checkOld := func() {
		cfg, meta, err := s.Load()
		if err != nil {
			t.Fatalf("Load after body write: %v", err)
		}
		if string(cfg) != "old" || meta.Generation != 1 {
			t.Fatalf("after body write Load = %q gen %d, want old gen 1", cfg, meta.Generation)
		}
		bodyBeforeCommit = true
	}
	s2 := NewStore(wrap(a, "a", checkOld), wrap(b, "b", checkOld))
	mustPublish(t, s2, []byte("new"))

	if len(ops) != 2 || ops[0] != "body" || ops[1] != "commit" {
		t.Fatalf("ops = %v, want [body commit]", ops)
	}
	if !bodyBeforeCommit {
		t.Fatal("hook did not run: body write not observed before commit")
	}
	cfg, meta, err := s2.Load()
	if err != nil || string(cfg) != "new" || meta.Generation != 2 {
		t.Fatalf("final Load = %q gen %d err %v", cfg, meta.Generation, err)
	}
}

type logBackend struct {
	inner     *memBackend
	name      string
	ops       *[]string
	afterBody func()
}

func (l *logBackend) Read() ([]byte, error) { return l.inner.Read() }

func (l *logBackend) WriteBody(image []byte) error {
	err := l.inner.WriteBody(image)
	*l.ops = append(*l.ops, "body")
	if l.afterBody != nil {
		l.afterBody()
	}
	return err
}

func (l *logBackend) WriteCommit() error {
	err := l.inner.WriteCommit()
	*l.ops = append(*l.ops, "commit")
	return err
}

// tearBackend simulates a crash that tears the body write after cut bytes.
type tearBackend struct {
	inner *memBackend
	cut   int
}

func (tb *tearBackend) Read() ([]byte, error) { return tb.inner.Read() }

func (tb *tearBackend) WriteBody(image []byte) error {
	tb.inner.data = append([]byte(nil), image[:tb.cut]...)
	return errors.New("simulated crash during body write")
}

func (tb *tearBackend) WriteCommit() error {
	return errors.New("unreachable: commit after crashed body")
}

// TestTornPublishEveryByte crashes the body write at every possible byte
// and requires the old configuration to remain readable and the torn slot
// to be rejected by the validator.
func TestTornPublishEveryByte(t *testing.T) {
	old := []byte("previous good config")
	for cut := 0; cut < SlotSize; cut++ {
		a, b := &memBackend{}, &memBackend{}
		s := NewStore(a, b)
		if _, err := s.Publish(old); err != nil {
			t.Fatalf("cut %d: seed publish: %v", cut, err)
		}
		// The next publish targets slot b; make its body write tear.
		s.slots[1] = &tearBackend{inner: b, cut: cut}
		if _, err := s.Publish([]byte("new config that tears")); err == nil {
			t.Fatalf("cut %d: torn publish succeeded", cut)
		}
		cfg, meta, err := s.Load()
		if err != nil {
			t.Fatalf("cut %d: Load after torn publish: %v", cut, err)
		}
		if !bytes.Equal(cfg, old) || meta.Generation != 1 {
			t.Fatalf("cut %d: Load = %q gen %d, want %q gen 1", cut, cfg, meta.Generation, old)
		}
		if _, _, err := ValidateSlot(b.data); err == nil {
			t.Fatalf("cut %d: validator accepted torn slot", cut)
		}
	}
}

// TestRecoveryAfterTornPublish ensures a clean publish succeeds after any
// number of torn attempts.
func TestRecoveryAfterTornPublish(t *testing.T) {
	a, b := &memBackend{}, &memBackend{}
	s := NewStore(a, b)
	mustPublish(t, s, []byte("gen1"))
	s.slots[1] = &tearBackend{inner: b, cut: 100}
	if _, err := s.Publish([]byte("torn")); err == nil {
		t.Fatal("torn publish succeeded")
	}
	s.slots[1] = b
	meta := mustPublish(t, s, []byte("gen2"))
	if meta.Generation != 2 {
		t.Fatalf("generation = %d, want 2", meta.Generation)
	}
	cfg, _ := mustLoad(t, s)
	if string(cfg) != "gen2" {
		t.Fatalf("Load = %q", cfg)
	}
}

// failCommitBackend fails the commit-flag write.
type failCommitBackend struct {
	inner *memBackend
}

func (f *failCommitBackend) Read() ([]byte, error)        { return f.inner.Read() }
func (f *failCommitBackend) WriteBody(image []byte) error { return f.inner.WriteBody(image) }
func (f *failCommitBackend) WriteCommit() error           { return errors.New("commit write failed") }

func TestCommitWriteFailureKeepsOld(t *testing.T) {
	a, b := &memBackend{}, &memBackend{}
	s := NewStore(a, b)
	mustPublish(t, s, []byte("old"))
	s.slots[1] = &failCommitBackend{inner: b}
	if _, err := s.Publish([]byte("new")); err == nil {
		t.Fatal("publish succeeded despite commit failure")
	}
	cfg, meta := mustLoad(t, s)
	if string(cfg) != "old" || meta.Generation != 1 {
		t.Fatalf("Load = %q gen %d", cfg, meta.Generation)
	}
	if _, _, err := ValidateSlot(b.data); err != ErrNotCommitted {
		t.Fatalf("slot b err = %v, want ErrNotCommitted", err)
	}
}

func TestGenerationOverflowRejected(t *testing.T) {
	a := &memBackend{data: committedSlot(math.MaxUint64, []byte("ancient"))}
	s := NewStore(a, &memBackend{})
	if _, err := s.Publish([]byte("overflow")); err != ErrGenerationOverflow {
		t.Fatalf("Publish err = %v, want ErrGenerationOverflow", err)
	}
	cfg, meta := mustLoad(t, s)
	if string(cfg) != "ancient" || meta.Generation != math.MaxUint64 {
		t.Fatalf("Load = %q gen %d", cfg, meta.Generation)
	}
}

// TestHighGenerationBadSlotIgnored places a broken slot with a far higher
// generation next to a valid older one and requires Load to return the old
// configuration in every broken variant.
func TestHighGenerationBadSlotIgnored(t *testing.T) {
	old := []byte("valid old config")
	good := committedSlot(2, old)

	uncommitted := encodeSlot(100, []byte("evil new"))
	badDigest := committedSlot(100, []byte("evil new"))
	badDigest[20] ^= 0x01
	badMagic := committedSlot(100, []byte("evil new"))
	badMagic[0] ^= 0xFF
	truncated := committedSlot(100, []byte("evil new"))[:SlotSize/2]
	garbage := []byte("not a slot at all")

	cases := map[string][]byte{
		"uncommitted": uncommitted,
		"bad digest":  badDigest,
		"bad magic":   badMagic,
		"truncated":   truncated,
		"garbage":     garbage,
		"absent":      nil,
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewStore(&memBackend{data: good}, &memBackend{data: broken})
			cfg, meta, err := s.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !bytes.Equal(cfg, old) || meta.Generation != 2 {
				t.Fatalf("Load = %q gen %d, want %q gen 2", cfg, meta.Generation, old)
			}
		})
	}
}

func TestPublishTooLarge(t *testing.T) {
	s := NewStore(&memBackend{}, &memBackend{})
	if _, err := s.Publish(make([]byte, MaxPayload+1)); err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := s.Publish(make([]byte, MaxPayload)); err != nil {
		t.Fatalf("max-size publish: %v", err)
	}
}

func TestFileBackendRoundtrip(t *testing.T) {
	dir, err := ioutil.TempDir("", "configstore")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	s := NewStore(
		&FileBackend{Path: filepath.Join(dir, "slot0")},
		&FileBackend{Path: filepath.Join(dir, "slot1")},
	)
	if _, _, err := s.Load(); err != ErrNoConfig {
		t.Fatalf("empty Load err = %v", err)
	}
	mustPublish(t, s, []byte("first"))
	mustPublish(t, s, []byte("second"))
	cfg, meta := mustLoad(t, s)
	if string(cfg) != "second" || meta.Generation != 2 {
		t.Fatalf("Load = %q gen %d", cfg, meta.Generation)
	}

	// Simulate a crash: write an uncommitted body directly into the slot
	// that will be used next, then verify Load still returns "second".
	next := filepath.Join(dir, "slot0")
	img := encodeSlot(3, []byte("crashed"))
	if err := ioutil.WriteFile(next, img, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, meta = mustLoad(t, s)
	if string(cfg) != "second" || meta.Generation != 2 {
		t.Fatalf("after crash Load = %q gen %d", cfg, meta.Generation)
	}
}
