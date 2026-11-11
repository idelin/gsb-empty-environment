package netsec

import (
	"errors"
	"testing"
)

// 全部 8 个 securebit 组成的掩码。
const allSecureBits SecureBits = SecNoroot | SecNorootLocked |
	SecNoSetuidFixup | SecNoSetuidFixupLocked |
	SecKeepCaps | SecKeepCapsLocked |
	SecNoCapAmbientRaise | SecNoCapAmbientRaiseLocked

// expectedSetSecureBits 是 SetSecureBits 的独立参考实现：当前已置位的
// 锁定位不可清除，且其冻结的基础位不可变化。
func expectedSetSecureBits(cur, next SecureBits) error {
	for _, pair := range lockedPairs {
		if cur.Has(pair.lock) {
			if !next.Has(pair.lock) {
				return ErrLockedSecureBit
			}
			if cur.Has(pair.base) != next.Has(pair.base) {
				return ErrLockedSecureBit
			}
		}
	}
	return nil
}

// TestSecureBitsLockingExhaustive 枚举全部 256 种当前状态 × 256 种目标
// 状态，核对锁定语义，并验证成功后任何已锁定的位都无法再被清除。
func TestSecureBitsLockingExhaustive(t *testing.T) {
	for cur := 0; cur <= int(allSecureBits); cur++ {
		for next := 0; next <= int(allSecureBits); next++ {
			curBits, nextBits := SecureBits(cur), SecureBits(next)
			p := Process{SecureBits: curBits}
			wantErr := expectedSetSecureBits(curBits, nextBits)
			err := p.SetSecureBits(nextBits)
			if !errors.Is(err, wantErr) {
				t.Fatalf("SetSecureBits(%08b -> %08b): got %v, want %v", curBits, nextBits, err, wantErr)
			}
			if err != nil {
				if p.SecureBits != curBits {
					t.Fatalf("failed SetSecureBits mutated state: %08b -> %08b", curBits, p.SecureBits)
				}
				continue
			}
			if p.SecureBits != nextBits {
				t.Fatalf("SetSecureBits state = %08b, want %08b", p.SecureBits, nextBits)
			}

			// 成功设置后，所有已置位的锁定位必须永久不可清除：尝试全部
			// 清零，应当失败且状态保持。
			if nextBits&(SecNorootLocked|SecNoSetuidFixupLocked|SecKeepCapsLocked|SecNoCapAmbientRaiseLocked) != 0 {
				before := p.SecureBits
				if err := p.SetSecureBits(0); !errors.Is(err, ErrLockedSecureBit) {
					t.Fatalf("clearing locked bits from %08b: got %v", before, err)
				}
				if p.SecureBits != before {
					t.Fatalf("locked bits were cleared: %08b -> %08b", before, p.SecureBits)
				}
			}
		}
	}
}

// TestSecureBitsLockLifecycle 走一遍“置基础位 → 加锁 → 基础位与锁均
// 冻结”的完整生命周期。
func TestSecureBitsLockLifecycle(t *testing.T) {
	p := Process{}

	if err := p.SetSecureBits(SecNoCapAmbientRaise); err != nil {
		t.Fatalf("raise base bit: %v", err)
	}
	// 未锁定时基础位仍可清除。
	if err := p.SetSecureBits(0); err != nil {
		t.Fatalf("clear unlocked base bit: %v", err)
	}

	// 基础位与锁定位同时置位。
	if err := p.SetSecureBits(SecNoCapAmbientRaise | SecNoCapAmbientRaiseLocked); err != nil {
		t.Fatalf("lock base bit: %v", err)
	}
	// 锁定后：只清基础位、只清锁、两者都清，全部失败。
	for _, target := range []SecureBits{
		SecNoCapAmbientRaiseLocked,
		SecNoCapAmbientRaise,
		0,
	} {
		before := p.SecureBits
		if err := p.SetSecureBits(target); !errors.Is(err, ErrLockedSecureBit) {
			t.Fatalf("target %08b: got %v", target, err)
		}
		if p.SecureBits != before {
			t.Fatalf("state changed under lock: %08b -> %08b", before, p.SecureBits)
		}
	}
	// 锁定位可以重复设置（保持不变），但无法解锁。
	if err := p.SetSecureBits(SecNoCapAmbientRaise | SecNoCapAmbientRaiseLocked); err != nil {
		t.Fatalf("re-applying same locked state: %v", err)
	}
}

// TestRaiseAmbientGating 核对 ambient 提升的前置条件与安全位闸门。
func TestRaiseAmbientGating(t *testing.T) {
	full := Set(0b111)

	// 同时在 P、I、B 中持有才能提升。
	p := Process{Permitted: full, Inheritable: full, Bounding: full}
	if err := p.RaiseAmbient(1); err != nil {
		t.Fatalf("RaiseAmbient: %v", err)
	}
	if !p.Ambient.Contains(1) {
		t.Fatalf("ambient not raised: %v", p.Ambient)
	}

	// 缺少任一集合中的能力即被拒绝，且不改变 ambient。
	denied := []Process{
		{Permitted: 0b101, Inheritable: full, Bounding: full},
		{Permitted: full, Inheritable: 0b101, Bounding: full},
		{Permitted: full, Inheritable: full, Bounding: 0b101},
	}
	for idx, bad := range denied {
		if err := bad.RaiseAmbient(1); !errors.Is(err, ErrAmbientNotRaisable) {
			t.Fatalf("case %d: got %v", idx, err)
		}
		if bad.Ambient != 0 {
			t.Fatalf("case %d: ambient mutated on failure", idx)
		}
	}

	// SECBIT_NO_CAP_AMBIENT_RAISE 置位时即使集合齐全也拒绝提升。
	blocked := Process{
		Permitted:   full,
		Inheritable: full,
		Bounding:    full,
		SecureBits:  SecNoCapAmbientRaise,
	}
	if err := blocked.RaiseAmbient(0); !errors.Is(err, ErrAmbientNotRaisable) {
		t.Fatalf("NO_CAP_AMBIENT_RAISE: got %v", err)
	}

	// 超界能力编号。
	if err := p.RaiseAmbient(MaxCaps); !errors.Is(err, ErrCapOutOfRange) {
		t.Fatalf("out of range: got %v", err)
	}

	// DropAmbient 只缩减集合。
	if err := p.DropAmbient(1); err != nil {
		t.Fatalf("DropAmbient: %v", err)
	}
	if p.Ambient.Contains(1) {
		t.Fatalf("ambient not dropped: %v", p.Ambient)
	}
}
