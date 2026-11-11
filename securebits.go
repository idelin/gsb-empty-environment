package netsec

// SecureBits 对应 prctl(2) 的 SECUREBITS 状态。每个基础位都有一个
// 配对的锁定位，锁定位一旦置位便无法清除，并冻结对应基础位。
type SecureBits uint32

// Linux securebit 定义，数值与内核 prctl(2) 一致。
const (
	SecNoroot                  SecureBits = 1 << 0
	SecNorootLocked            SecureBits = 1 << 1
	SecNoSetuidFixup           SecureBits = 1 << 2
	SecNoSetuidFixupLocked     SecureBits = 1 << 3
	SecKeepCaps                SecureBits = 1 << 4
	SecKeepCapsLocked          SecureBits = 1 << 5
	SecNoCapAmbientRaise       SecureBits = 1 << 6
	SecNoCapAmbientRaiseLocked SecureBits = 1 << 7
)

// lockedPairs 给出每个锁定位与其冻结的基础位。
var lockedPairs = []struct{ lock, base SecureBits }{
	{SecNorootLocked, SecNoroot},
	{SecNoSetuidFixupLocked, SecNoSetuidFixup},
	{SecKeepCapsLocked, SecKeepCaps},
	{SecNoCapAmbientRaiseLocked, SecNoCapAmbientRaise},
}

// Has 判断是否置了某个 securebit。
func (s SecureBits) Has(bit SecureBits) bool { return s&bit != 0 }

// SetSecureBits 模拟 prctl(PR_SET_SECUREBITS)。
//
// 锁定规则：
//   - 已置位的锁定位不能清除；
//   - 锁定位置位时，其配对的基础位被冻结，不能置位也不能清除。
//
// 违反任一规则返回 ErrLockedSecureBit，状态保持不变。
func (p *Process) SetSecureBits(next SecureBits) error {
	cur := p.SecureBits
	for _, pair := range lockedPairs {
		if cur.Has(pair.lock) {
			// 锁定位自身不可清除。
			if !next.Has(pair.lock) {
				return ErrLockedSecureBit
			}
			// 被冻结的基础位不可变化。
			if cur.Has(pair.base) != next.Has(pair.base) {
				return ErrLockedSecureBit
			}
		}
	}
	p.SecureBits = next
	return nil
}

// RaiseAmbient 模拟 prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE)。
//
// 提升前提（capabilities(7)）：进程在 P、I、B 中均持有该能力，且未置
// SECBIT_NO_CAP_AMBIENT_RAISE。成功后 ambient 集合加入该能力。
func (p *Process) RaiseAmbient(c int) error {
	if c < 0 || c >= MaxCaps {
		return ErrCapOutOfRange
	}
	if p.SecureBits.Has(SecNoCapAmbientRaise) {
		return ErrAmbientNotRaisable
	}
	bit := Set(1) << uint(c)
	if p.Permitted&bit == 0 || p.Inheritable&bit == 0 || p.Bounding&bit == 0 {
		return ErrAmbientNotRaisable
	}
	p.Ambient |= bit
	return nil
}

// DropAmbient 模拟 PR_CAP_AMBIENT_DROP，移除单个 ambient 能力。
func (p *Process) DropAmbient(c int) error {
	if c < 0 || c >= MaxCaps {
		return ErrCapOutOfRange
	}
	p.Ambient &^= Set(1) << uint(c)
	return nil
}
