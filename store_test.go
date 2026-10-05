package pubstore

import (
	"bytes"
	"errors"
	"math"
	"os"
	"testing"
)

func openTempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func mustPublish(t *testing.T, st *Store, data string) *Config {
	t.Helper()
	cfg, err := st.Publish([]byte(data))
	if err != nil {
		t.Fatalf("Publish(%q): %v", data, err)
	}
	return cfg
}

func mustLoad(t *testing.T, st *Store) *Config {
	t.Helper()
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestPublishLoadRoundTripAndAlternation(t *testing.T) {
	st := openTempStore(t)

	if _, err := st.Load(); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("empty store Load err = %v, want ErrNoConfig", err)
	}

	c1 := mustPublish(t, st, "alpha")
	c2 := mustPublish(t, st, "beta")
	c3 := mustPublish(t, st, "gamma")
	if c1.Generation != 1 || c2.Generation != 2 || c3.Generation != 3 {
		t.Fatalf("generations = %d,%d,%d, want 1,2,3", c1.Generation, c2.Generation, c3.Generation)
	}

	got := mustLoad(t, st)
	if got.Generation != 3 || string(got.Data) != "gamma" {
		t.Fatalf("Load = gen %d %q, want gen 3 %q", got.Generation, got.Data, "gamma")
	}

	// 相邻代际必须落在不同槽位：slot0 持 gen3，slot1 持 gen2。
	s0, err := readSlot(st.slots[0])
	if err != nil || s0.Generation != 3 {
		t.Fatalf("slot0 = gen %d err %v, want gen 3", genOf(s0), err)
	}
	s1, err := readSlot(st.slots[1])
	if err != nil || s1.Generation != 2 {
		t.Fatalf("slot1 = gen %d err %v, want gen 2", genOf(s1), err)
	}
}

func genOf(s *Slot) uint64 {
	if s == nil {
		return 0
	}
	return s.Generation
}

// 对最新槽做逐字节截断：任何截断点都必须回退到旧配置，
// 绝不能读到撕裂的新值。
func TestTruncationEveryByteOfLatestSlot(t *testing.T) {
	st := openTempStore(t)
	mustPublish(t, st, "old-config")
	mustPublish(t, st, "new-config-v2")

	latestPath := st.slots[1]
	full, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	for n := 0; n < len(full); n++ {
		if err := os.WriteFile(latestPath, full, 0o600); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if err := os.Truncate(latestPath, int64(n)); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		cfg, err := st.Load()
		if err != nil {
			t.Fatalf("truncate to %d: Load: %v", n, err)
		}
		if cfg.Generation != 1 || string(cfg.Data) != "old-config" {
			t.Fatalf("truncate to %d: got gen %d %q, want gen 1 old-config",
				n, cfg.Generation, cfg.Data)
		}
	}

	// 恢复完整镜像后应再次读到新值。
	if err := os.WriteFile(latestPath, full, 0o600); err != nil {
		t.Fatalf("restore: %v", err)
	}
	cfg := mustLoad(t, st)
	if cfg.Generation != 2 || string(cfg.Data) != "new-config-v2" {
		t.Fatalf("restored: got gen %d %q", cfg.Generation, cfg.Data)
	}
}

// 只有一个有效槽时，逐字节截断必须导致 ErrNoConfig 而不是半个配置。
func TestTruncationEveryByteOfOnlySlot(t *testing.T) {
	st := openTempStore(t)
	mustPublish(t, st, "only-config")

	full, err := os.ReadFile(st.slots[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for n := 0; n < len(full); n++ {
		if err := os.WriteFile(st.slots[0], full, 0o600); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if err := os.Truncate(st.slots[0], int64(n)); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		if _, err := st.Load(); !errors.Is(err, ErrNoConfig) {
			t.Fatalf("truncate to %d: Load err = %v, want ErrNoConfig", n, err)
		}
	}
}

// 提交标记必须最后写入：标记写入前崩溃，发布失败且旧配置可读。
func TestCommitMarkerWrittenLast(t *testing.T) {
	st := openTempStore(t)
	mustPublish(t, st, "stable")

	st.crashBeforeCommit = true
	if _, err := st.Publish([]byte("torn-value")); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("Publish err = %v, want simulated crash", err)
	}

	cfg := mustLoad(t, st)
	if cfg.Generation != 1 || string(cfg.Data) != "stable" {
		t.Fatalf("after crash: got gen %d %q, want gen 1 stable", cfg.Generation, cfg.Data)
	}

	// 崩溃留下的未提交槽必须能被后续发布覆盖恢复。
	st.crashBeforeCommit = false
	c := mustPublish(t, st, "recovered")
	if c.Generation != 2 {
		t.Fatalf("recovered generation = %d, want 2", c.Generation)
	}
	if got := mustLoad(t, st); string(got.Data) != "recovered" {
		t.Fatalf("Load = %q, want recovered", got.Data)
	}
}

// 未提交镜像即使头部、长度、摘要全部正确也必须被拒绝；
// 只有最后落下的提交标记能使其生效——这验证了标记的次序语义。
func TestCommitMarkerGatesValidity(t *testing.T) {
	payload := []byte("gated-config")
	image := encodeSlot(7, payload, false)
	if _, err := ValidateSlot(image); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("uncommitted image err = %v, want ErrInvalidSlot", err)
	}
	image[commitOff] = commitCommitted
	slot, err := ValidateSlot(image)
	if err != nil {
		t.Fatalf("committed image: %v", err)
	}
	if slot.Generation != 7 || !bytes.Equal(slot.Payload, payload) {
		t.Fatalf("slot = gen %d %q", slot.Generation, slot.Payload)
	}
}

// 代际溢出必须明确拒绝，且已有配置仍然可读。
func TestGenerationOverflowRejected(t *testing.T) {
	st := openTempStore(t)
	image := encodeSlot(math.MaxUint64, []byte("max-gen"), true)
	if err := os.WriteFile(st.slots[0], image, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := st.Publish([]byte("would-wrap")); !errors.Is(err, ErrGenerationOverflow) {
		t.Fatalf("Publish err = %v, want ErrGenerationOverflow", err)
	}
	cfg := mustLoad(t, st)
	if cfg.Generation != math.MaxUint64 || string(cfg.Data) != "max-gen" {
		t.Fatalf("Load = gen %d %q", cfg.Generation, cfg.Data)
	}
}

// 高代际坏槽绝不能被当成新值：各种损坏方式下都必须读到旧配置。
func TestCorruptHighGenerationSlotIgnored(t *testing.T) {
	corruptors := map[string]func([]byte){
		"flip payload byte": func(b []byte) { b[payloadOff] ^= 0xFF },
		"flip digest byte":  func(b []byte) { b[digestOff] ^= 0xFF },
		"flip magic":        func(b []byte) { b[0] ^= 0xFF },
		"clear commit":      func(b []byte) { b[commitOff] = 0x00 },
		"garbage commit":    func(b []byte) { b[commitOff] = 0x01 },
		"truncate tail":     func(b []byte) { /* 单独处理 */ },
	}
	for name, corrupt := range corruptors {
		t.Run(name, func(t *testing.T) {
			st := openTempStore(t)
			mustPublish(t, st, "good")
			evil := encodeSlot(999, []byte("evil-high-gen"), true)
			if name == "truncate tail" {
				evil = evil[:len(evil)-3]
			} else {
				corrupt(evil)
			}
			if err := os.WriteFile(st.slots[1], evil, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			cfg := mustLoad(t, st)
			if cfg.Generation != 1 || string(cfg.Data) != "good" {
				t.Fatalf("got gen %d %q, want gen 1 good", cfg.Generation, cfg.Data)
			}
			// 坏槽不得参与代际推进：下一次发布仍从 gen 1 递增。
			c := mustPublish(t, st, "next")
			if c.Generation != 2 {
				t.Fatalf("next generation = %d, want 2", c.Generation)
			}
		})
	}
}

// 独立槽验证器：不经过 Store，直接对字节镜像做表驱动判定。
func TestValidateSlotIndependent(t *testing.T) {
	payload := []byte("validator-payload")
	valid := encodeSlot(42, payload, true)

	withDigest := func(mutate func([]byte)) []byte {
		b := append([]byte(nil), valid...)
		mutate(b)
		gen := uint64(42)
		length := uint32(len(payload))
		d := slotDigest(gen, length, b[payloadOff:])
		copy(b[digestOff:], d[:])
		return b
	}

	cases := []struct {
		name    string
		image   []byte
		wantErr bool
	}{
		{"valid", valid, false},
		{"empty", nil, true},
		{"header minus one", valid[:headerSize-1], true},
		{"bad magic", withDigest(func(b []byte) { b[0] = 'X' }), true},
		{"uncommitted", encodeSlot(42, payload, false), true},
		{"garbage commit", withDigest(func(b []byte) { b[commitOff] = 0x5A }), true},
		{"length too large", withDigest(func(b []byte) {
			b[lengthOff] = 0xFF
			b[lengthOff+1] = 0xFF
			b[lengthOff+2] = 0xFF
			b[lengthOff+3] = 0x7F
		}), true},
		{"length off by one", withDigest(func(b []byte) { b[lengthOff]++ }), true},
		{"trailing garbage", append(append([]byte(nil), valid...), 0x00), true},
		{"payload flipped", func() []byte {
			b := append([]byte(nil), valid...)
			b[payloadOff] ^= 0x01
			return b
		}(), true},
		{"digest flipped", func() []byte {
			b := append([]byte(nil), valid...)
			b[digestOff+5] ^= 0x01
			return b
		}(), true},
		{"generation torn", func() []byte {
			b := append([]byte(nil), valid...)
			b[generationOff] ^= 0x01
			return b
		}(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slot, err := ValidateSlot(tc.image)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidSlot) {
					t.Fatalf("err = %v, want ErrInvalidSlot", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if slot.Generation != 42 || !bytes.Equal(slot.Payload, payload) {
				t.Fatalf("slot = gen %d %q", slot.Generation, slot.Payload)
			}
		})
	}
}

// 发布失败（载荷超限）不得影响旧配置。
func TestFailedPublishKeepsOldConfig(t *testing.T) {
	st := openTempStore(t)
	mustPublish(t, st, "keep-me")

	if _, err := st.Publish(make([]byte, MaxPayload+1)); err == nil {
		t.Fatal("oversize Publish: want error")
	}
	cfg := mustLoad(t, st)
	if cfg.Generation != 1 || string(cfg.Data) != "keep-me" {
		t.Fatalf("got gen %d %q, want gen 1 keep-me", cfg.Generation, cfg.Data)
	}
}

// 槽文件被整体删除时仍可从另一槽读取。
func TestMissingSlotFile(t *testing.T) {
	st := openTempStore(t)
	mustPublish(t, st, "v1")
	mustPublish(t, st, "v2")
	if err := os.Remove(st.slots[1]); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	cfg := mustLoad(t, st)
	if cfg.Generation != 1 || string(cfg.Data) != "v1" {
		t.Fatalf("got gen %d %q, want gen 1 v1", cfg.Generation, cfg.Data)
	}
}
