package netsec

import (
	"errors"
	"testing"
)

// 合成指令集（仅用于测试，刻意做成变长且与 x86 相似的小端相对位移模型）：
//   - 0x00             NOP，定长 1 字节，无分支
//   - 0x01 L pp...     变长指令，总长度 2+L（L 为负载字节数），无分支
//   - 0x10 rel8        短跳，2 字节，目标 = 指令末尾 + int8(rel8)
//   - 0x20 rel32(LE)   近跳，5 字节，目标 = 指令末尾 + int32(rel32)
const (
	synNOP   = 0x00
	synVAR   = 0x01
	synSJMP  = 0x10
	synJMP32 = 0x20
)

// synthImage 是合成解码器，自带一份镜像字节。
type synthImage struct {
	base uint64
	mem  []byte
}

func (s *synthImage) DecodeAt(addr uint64) (Instr, error) {
	if addr < s.base {
		return Instr{}, errors.New("addr below image base")
	}
	idx := int(addr - s.base)
	if idx >= len(s.mem) {
		return Instr{}, errors.New("addr past image end")
	}
	op := s.mem[idx]
	switch op {
	case synNOP:
		return Instr{Addr: addr, Bytes: append([]byte(nil), s.mem[idx:idx+1]...)}, nil
	case synVAR:
		if idx+2 > len(s.mem) {
			return Instr{}, errors.New("truncated VAR length byte")
		}
		n := 2 + int(s.mem[idx+1])
		if idx+n > len(s.mem) {
			return Instr{}, errors.New("truncated VAR payload")
		}
		return Instr{Addr: addr, Bytes: append([]byte(nil), s.mem[idx:idx+n]...)}, nil
	case synSJMP:
		const n = 2
		if idx+n > len(s.mem) {
			return Instr{}, errors.New("truncated short jump")
		}
		end := addr + n
		target := uint64(int64(end) + int64(int8(s.mem[idx+1])))
		return Instr{
			Addr:   addr,
			Bytes:  append([]byte(nil), s.mem[idx:idx+n]...),
			Branch: &Branch{Target: target, DispSize: 1, DispEnd: 2},
		}, nil
	case synJMP32:
		const n = 5
		if idx+n > len(s.mem) {
			return Instr{}, errors.New("truncated near jump")
		}
		rel := int32(s.mem[idx+1]) | int32(s.mem[idx+2])<<8 |
			int32(s.mem[idx+3])<<16 | int32(s.mem[idx+4])<<24
		end := addr + n
		target := uint64(int64(end) + int64(rel))
		return Instr{
			Addr:   addr,
			Bytes:  append([]byte(nil), s.mem[idx:idx+n]...),
			Branch: &Branch{Target: target, DispSize: 4, DispEnd: 5},
		}, nil
	default:
		return Instr{}, errors.New("unknown opcode")
	}
}

// synthJumper 只编码 5 字节近跳，rel32 超出 int32 或 max 不足时失败。
type synthJumper struct{}

func (synthJumper) EncodeJump(addr, target uint64, max int) ([]byte, error) {
	const size = 5
	if max > 0 && max < size {
		return nil, errors.Join(ErrJumpRange, errors.New("region too small for 5-byte jump"))
	}
	rel := int64(target) - int64(addr+size)
	if rel < -(1<<31) || rel > (1<<31)-1 {
		return nil, errors.Join(ErrJumpRange, errors.New("rel32 overflow"))
	}
	out := make([]byte, size)
	out[0] = synJMP32
	for i := 0; i < 4; i++ {
		out[1+i] = byte(rel >> (8 * uint(i)))
	}
	return out, nil
}

// synthNoper 生成全 0x00 的 NOP 序列（合成 ISA 中 0x00 即 NOP）。
type synthNoper struct{}

func (synthNoper) EncodeNop(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("negative nop length")
	}
	return make([]byte, n), nil
}

// memWriter 是记录型 Writer：统计写入次数，可在第 failOn 次写入（1 起算）
// 注入失败，便于验证失败路径不产生后续写入。
type memWriter struct {
	base   uint64
	mem    []byte
	writes int
	failOn int
}

func (w *memWriter) WriteAt(addr uint64, p []byte) error {
	w.writes++
	if w.failOn != 0 && w.writes == w.failOn {
		return errors.New("injected write failure")
	}
	idx := int(addr - w.base)
	if idx < 0 || idx+len(p) > len(w.mem) {
		return errors.New("write out of range")
	}
	copy(w.mem[idx:idx+len(p)], p)
	return nil
}

func newImage(base uint64, size int) *synthImage {
	return &synthImage{base: base, mem: make([]byte, size)}
}

func putRel8(img *synthImage, at uint64, rel int8) {
	img.mem[int(at-img.base)] = synSJMP
	img.mem[int(at-img.base)+1] = byte(rel)
}

// putRel32 在 at 处写入一条合成近跳，目标为 target。
func putRel32(t *testing.T, img *synthImage, at, target uint64) {
	t.Helper()
	rel := int64(target) - int64(at+5)
	if rel < -(1<<31) || rel > (1<<31)-1 {
		t.Fatalf("rel32 %d out of range in test setup", rel)
	}
	off := int(at - img.base)
	img.mem[off] = synJMP32
	for i := 0; i < 4; i++ {
		img.mem[off+1+i] = byte(rel >> (8 * uint(i)))
	}
}

// TestNearJumpRelocatedOK：rel32 近跳搬到跳板后位移重算为 4 字节小端值，
// 目标地址保持不变。
func TestNearJumpRelocatedOK(t *testing.T) {
	const base = 0x1000
	img := newImage(base, 0x2000)
	putRel32(t, img, base, 0x4000) // 近跳指向远处目标

	plan, err := Build(Config{
		Entry:       base,
		MinPatchLen: 5,
		Decoder:     img,
		Jump:        synthJumper{},
		Nop:         synthNoper{},
		TrampAddr:   0x2000,
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if plan.StolenLen != 5 {
		t.Fatalf("StolenLen = %d, want 5", plan.StolenLen)
	}

	// 跳板前 5 字节：0x2000 处近跳副本，新 rel32 = 0x4000-0x2005 = 0x1FFB。
	wantRel := int64(0x4000 - 0x2005)
	gotRel := int32(plan.Trampoline[1]) | int32(plan.Trampoline[2])<<8 |
		int32(plan.Trampoline[3])<<16 | int32(plan.Trampoline[4])<<24
	if int64(gotRel) != wantRel {
		t.Fatalf("relocated rel32 = %d, want %d", gotRel, wantRel)
	}

	// 跳板回跳：0x200A -> 0x1005，rel32 = -4099 = 0xFFFF0FFD。
	backRel := int64(0x1005 - 0x200A)
	gotBack := int32(plan.Trampoline[6]) | int32(plan.Trampoline[7])<<8 |
		int32(plan.Trampoline[8])<<16 | int32(plan.Trampoline[9])<<24
	if int64(gotBack) != backRel {
		t.Fatalf("jump-back rel32 = %d, want %d", gotBack, backRel)
	}

	// 原镜像的 rel32 不允许被就地修改。
	origRel := int64(0x4000 - 0x1005)
	for i := 0; i < 4; i++ {
		if want := byte(origRel >> (8 * uint(i))); img.mem[1+i] != want {
			t.Fatalf("original near jump displacement changed at byte %d: % x", i, img.mem[0:5])
		}
	}
}

// TestShortJumpOverflow：短跳搬到远处跳板后 rel8 无法容纳新位移，
// Build 必须失败并返回 ErrDispRange，且调用方镜像一个字节都不能被改动。
func TestShortJumpOverflow(t *testing.T) {
	const base = 0x1000
	img := newImage(base, 0x200)
	putRel8(img, base, 120) // 目标 base+2+120 = 0x107A
	img.mem[2] = synNOP

	snapshot := append([]byte(nil), img.mem...)

	_, err := Build(Config{
		Entry:       base,
		MinPatchLen: 3,
		Decoder:     img,
		Jump:        synthJumper{},
		Nop:         synthNoper{},
		TrampAddr:   0x2000,
	})
	if !errors.Is(err, ErrDispRange) {
		t.Fatalf("want ErrDispRange, got %v", err)
	}
	if string(img.mem) != string(snapshot) {
		t.Fatal("failed Build mutated decoder image bytes")
	}
}

// TestShortJumpRelocatedOK：跳板足够近时短跳重定位成功，
// 校验重定位后的 rel8、跳板回跳、入口跳转填充以及原始字节不被改写。
func TestShortJumpRelocatedOK(t *testing.T) {
	const base = 0x1000
	img := newImage(base, 0x200)
	putRel8(img, base, 62) // 目标 0x1002+62 = 0x1040
	img.mem[2] = synNOP
	img.mem[3] = synVAR // 4 字节变长指令：01 02 EE EE
	img.mem[4] = 2
	img.mem[5] = 0xEE
	img.mem[6] = 0xEE

	plan, err := Build(Config{
		Entry:       base,
		MinPatchLen: 5, // 5 字节入口跳转会把覆盖长度抬到整条指令边界 7
		Decoder:     img,
		Jump:        synthJumper{},
		Nop:         synthNoper{},
		TrampAddr:   0x1080,
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if plan.StolenLen != 7 {
		t.Fatalf("StolenLen = %d, want 7 (instruction boundary)", plan.StolenLen)
	}
	if len(plan.Patch) != 7 {
		t.Fatalf("patch length = %d, want 7", len(plan.Patch))
	}

	// 入口跳转：0x1000 -> 0x1080，rel32 = 0x1080-0x1005 = 123，后跟 2 字节 NOP。
	wantPatch := []byte{synJMP32, 123, 0, 0, 0, synNOP, synNOP}
	if string(plan.Patch) != string(wantPatch) {
		t.Fatalf("patch = % x, want % x", plan.Patch, wantPatch)
	}

	// 跳板布局（12 字节）：
	//   0x1080: 短跳副本，新 rel8 = 0x1040-0x1082 = -66
	//   0x1082: NOP 副本
	//   0x1083: VAR 副本（01 02 EE EE）
	//   0x1087: 回跳 0x108C -> 0x1007，rel32 = -133
	wantTramp := []byte{
		synSJMP, 0xBE, // int8(-66) 的无符号字节表示
		synNOP,
		synVAR, 2, 0xEE, 0xEE,
		synJMP32, 0x7B, 0xFF, 0xFF, 0xFF,
	}
	if string(plan.Trampoline) != string(wantTramp) {
		t.Fatalf("trampoline = % x, want % x", plan.Trampoline, wantTramp)
	}

	// 规划不得改写解码器返回的原始镜像。
	if img.mem[1] != byte(62) {
		t.Fatalf("original rel8 mutated: %d", img.mem[1])
	}

	// Apply 成功后两个区间都应落盘。
	w := &memWriter{base: base, mem: make([]byte, 0x200)}
	if err := plan.Apply(w); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if w.writes != 2 {
		t.Fatalf("writes = %d, want 2 (trampoline then patch)", w.writes)
	}
	if got := w.mem[0:7]; string(got) != string(wantPatch) {
		t.Fatalf("entry bytes = % x, want % x", got, wantPatch)
	}
	if got := w.mem[0x80 : 0x80+12]; string(got) != string(wantTramp) {
		t.Fatalf("trampoline bytes = % x, want % x", got, wantTramp)
	}
}

// TestInteriorTargetRejected：分支目标或已知入口落在覆盖区内部必须拒绝。
func TestInteriorTargetRejected(t *testing.T) {
	const base = 0x1000

	t.Run("branch target interior", func(t *testing.T) {
		img := newImage(base, 0x100)
		putRel8(img, base, 2) // 目标 0x1004，严格落在覆盖区 [0x1000,0x1005) 内部
		_, err := Build(Config{
			Entry:       base,
			MinPatchLen: 5,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   0x1080,
		})
		if !errors.Is(err, ErrInteriorTarget) {
			t.Fatalf("want ErrInteriorTarget, got %v", err)
		}
	})

	t.Run("known target interior", func(t *testing.T) {
		img := newImage(base, 0x100) // 全 NOP，无分支
		_, err := Build(Config{
			Entry:        base,
			MinPatchLen:  5,
			Decoder:      img,
			Jump:         synthJumper{},
			Nop:          synthNoper{},
			TrampAddr:    0x2000,
			KnownTargets: []uint64{base + 2},
		})
		if !errors.Is(err, ErrInteriorTarget) {
			t.Fatalf("want ErrInteriorTarget, got %v", err)
		}
	})
}

// TestInteriorBoundaryAccepted：落在覆盖区两个端点上的目标是合法的：
// 入口自身（区间左端）与覆盖区之后第一条指令（区间右端）。
func TestInteriorBoundaryAccepted(t *testing.T) {
	const base = 0x1000

	t.Run("target at region end", func(t *testing.T) {
		img := newImage(base, 0x100)
		putRel8(img, base, 3) // 目标 0x1005 == regionEnd
		if _, err := Build(Config{
			Entry:       base,
			MinPatchLen: 5,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   0x1080,
		}); err != nil {
			t.Fatalf("boundary target at region end rejected: %v", err)
		}
	})

	t.Run("target at entry itself", func(t *testing.T) {
		img := newImage(base, 0x100)
		putRel8(img, base, -2) // 自跳，目标 == Entry
		if _, err := Build(Config{
			Entry:       base,
			MinPatchLen: 5,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   0x1040,
		}); err != nil {
			t.Fatalf("boundary target at entry rejected: %v", err)
		}
	})
}

// TestDecodeTruncation：镜像在指令中间结束时解码失败，规划中止，
// 绝不为凑够长度而截断一条变长指令。
func TestDecodeTruncation(t *testing.T) {
	const base = 0x1000

	t.Run("truncated short jump", func(t *testing.T) {
		img := &synthImage{base: base, mem: []byte{synSJMP}}
		_, err := Build(Config{
			Entry:       base,
			MinPatchLen: 2,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   0x2000,
		})
		if !errors.Is(err, ErrDecode) {
			t.Fatalf("want ErrDecode, got %v", err)
		}
	})

	t.Run("truncated variable instruction", func(t *testing.T) {
		img := &synthImage{base: base, mem: []byte{synVAR, 3, 0xAA}}
		_, err := Build(Config{
			Entry:       base,
			MinPatchLen: 2,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   0x2000,
		})
		if !errors.Is(err, ErrDecode) {
			t.Fatalf("want ErrDecode, got %v", err)
		}
	})

	t.Run("unknown opcode", func(t *testing.T) {
		img := &synthImage{base: base, mem: []byte{0x7F}}
		_, err := Build(Config{
			Entry:       base,
			MinPatchLen: 1,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   0x2000,
		})
		if !errors.Is(err, ErrDecode) {
			t.Fatalf("want ErrDecode, got %v", err)
		}
	})
}

// TestJumpOutOfReach：跳板远到 rel32 无法编码时返回 ErrJumpRange，
// 不产生任何规划产物。
func TestJumpOutOfReach(t *testing.T) {
	const base = 0x1000
	img := newImage(base, 0x100) // 全 NOP
	_, err := Build(Config{
		Entry:       base,
		MinPatchLen: 5,
		Decoder:     img,
		Jump:        synthJumper{},
		Nop:         synthNoper{},
		TrampAddr:   base + 1<<40,
	})
	if !errors.Is(err, ErrJumpRange) {
		t.Fatalf("want ErrJumpRange, got %v", err)
	}
}

// TestApplyFailureWritesNothing：跳板写入失败时入口覆盖区不得被触碰；
// 跳板写入成功而入口写入失败时，写入次数恰好为 2 且跳板保持已就位内容。
func TestApplyFailureWritesNothing(t *testing.T) {
	const base = 0x1000
	img := newImage(base, 0x200)
	plan, err := Build(Config{
		Entry:       base,
		MinPatchLen: 5,
		Decoder:     img,
		Jump:        synthJumper{},
		Nop:         synthNoper{},
		TrampAddr:   0x2000,
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	t.Run("trampoline write fails", func(t *testing.T) {
		before := make([]byte, 0x2000)
		w := &memWriter{base: base, mem: append([]byte(nil), before...), failOn: 1}
		if err := plan.Apply(w); !errors.Is(err, ErrWrite) {
			t.Fatalf("want ErrWrite, got %v", err)
		}
		if w.writes != 1 {
			t.Fatalf("writes = %d, want 1", w.writes)
		}
		if string(w.mem) != string(before) {
			t.Fatal("entry region must stay untouched when trampoline write fails")
		}
	})

	t.Run("entry write fails after trampoline", func(t *testing.T) {
		w := &memWriter{
			base:   base,
			mem:    make([]byte, 0x2000),
			failOn: 2,
		}
		err := plan.Apply(w)
		if !errors.Is(err, ErrWrite) {
			t.Fatalf("want ErrWrite, got %v", err)
		}
		if w.writes != 2 {
			t.Fatalf("writes = %d, want 2", w.writes)
		}
		// 跳板已完整落盘，入口区仍为零。
		trampOff := int(plan.TrampAddr - base)
		if got := w.mem[trampOff : trampOff+len(plan.Trampoline)]; string(got) != string(plan.Trampoline) {
			t.Fatal("trampoline not in place after entry write failure")
		}
		if got := w.mem[:plan.StolenLen]; string(got) != string(make([]byte, plan.StolenLen)) {
			t.Fatal("entry region must stay untouched when entry write fails")
		}
	})
}

// TestConfigValidation：非法配置在任何解码与写入之前被拒绝。
func TestConfigValidation(t *testing.T) {
	img := newImage(0x1000, 0x100)
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero min length", Config{Entry: 0x1000, MinPatchLen: 0, Decoder: img, Jump: synthJumper{}}},
		{"nil decoder", Config{Entry: 0x1000, MinPatchLen: 5, Jump: synthJumper{}}},
		{"nil jump encoder", Config{Entry: 0x1000, MinPatchLen: 5, Decoder: img}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Build(tc.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
	if err := (&Plan{}).Apply(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Apply(nil) want ErrInvalidConfig, got %v", err)
	}
}

// TestEntryPaddingAndOverlap：入口跳转短于覆盖区且未提供 NOP 编码器时
// 返回 ErrNoNop；跳板与覆盖区相交时返回 ErrOverlap。
func TestEntryPaddingAndOverlap(t *testing.T) {
	const base = 0x1000

	t.Run("nop encoder missing", func(t *testing.T) {
		img := newImage(base, 0x100)
		_, err := Build(Config{
			Entry:       base,
			MinPatchLen: 8, // 全 NOP 累积到 8 字节，入口跳转只占 5 字节
			Decoder:     img,
			Jump:        synthJumper{},
			TrampAddr:   0x2000,
		})
		if !errors.Is(err, ErrNoNop) {
			t.Fatalf("want ErrNoNop, got %v", err)
		}
	})

	t.Run("trampoline overlaps region", func(t *testing.T) {
		img := newImage(base, 0x100)
		_, err := Build(Config{
			Entry:       base,
			MinPatchLen: 5,
			Decoder:     img,
			Jump:        synthJumper{},
			Nop:         synthNoper{},
			TrampAddr:   base + 2, // 落在覆盖区 [base, base+5) 内部
		})
		if !errors.Is(err, ErrOverlap) {
			t.Fatalf("want ErrOverlap, got %v", err)
		}
	})
}
