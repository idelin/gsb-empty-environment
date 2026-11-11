package netsec

import "errors"

// MaxCaps 是本包可表达的能力编号上界（0..MaxCaps-1），覆盖 Linux 当前
// 命名的全部 capability。
const MaxCaps = 64

// Set 是一个能力集合，第 c 位置 1 表示持有 capability c。
type Set uint64

// FullSet 返回本包位宽内的能力全集。
func FullSet() Set { return ^Set(0) }

// Empty 判断集合是否为空。
func (s Set) Empty() bool { return s == 0 }

// Contains 判断是否持有能力 c。
func (s Set) Contains(c int) bool {
	if c < 0 || c >= MaxCaps {
		return false
	}
	return s&(Set(1)<<uint(c)) != 0
}

// Add 返回加入能力 c 后的集合。
func (s Set) Add(c int) (Set, error) {
	if c < 0 || c >= MaxCaps {
		return s, ErrCapOutOfRange
	}
	return s | (Set(1) << uint(c)), nil
}

// IsSubset 判断 s 是否为 other 的子集。
func (s Set) IsSubset(other Set) bool { return s&^other == 0 }

// Union 返回并集。
func (s Set) Union(other Set) Set { return s | other }

// Intersect 返回交集。
func (s Set) Intersect(other Set) Set { return s & other }

// Process 是 exec 前进程的能力状态与安全上下文。
type Process struct {
	// Permitted 是进程 permitted 集合 P。
	Permitted Set
	// Inheritable 是进程 inheritable 集合 I。
	Inheritable Set
	// Ambient 是进程 ambient 集合 A。
	Ambient Set
	// Bounding 是能力边界集合 B。
	Bounding Set
	// Effective 是进程当前 effective 集合（exec 后按文件标记重算）。
	Effective Set
	// SecureBits 是进程当前 securebit 状态。
	SecureBits SecureBits
	// NoNewPrivs 对应 PR_SET_NO_NEW_PRIVS，置位后禁止经 exec 提权。
	NoNewPrivs bool
}

// File 是被执行文件的能力属性。
type File struct {
	// Permitted 是文件 permitted 能力集合 F_p。
	Permitted Set
	// Inheritable 是文件 inheritable 能力集合 F_i。
	Inheritable Set
	// Effective 是文件 effective 标记：置位时新 effective 取新 permitted，
	// 否则只取 ambient。
	Effective bool
	// SetUID 对应 set-user-ID 模式位。
	SetUID bool
	// SetGID 对应 set-group-ID 模式位。
	SetGID bool
	// UserNamespaceMapped 表示文件能力的 root 映射是否存在于当前用户
	// 命名空间。映射失败时文件能力按空集合处理。
	UserNamespaceMapped bool
}

// Privileged 判断文件是否为“特权文件”：携带文件能力，或置了
// set-user-ID / set-group-ID 位。映射失败的文件能力视为不存在。
func (f File) Privileged() bool {
	if f.SetUID || f.SetGID {
		return true
	}
	if !f.UserNamespaceMapped {
		return false
	}
	return !(f.Permitted.Empty() && f.Inheritable.Empty())
}

// EffectiveFileCaps 返回转换时实际生效的文件能力。用户命名空间映射失败
// 时返回空集合，即“无文件能力”。
func (f File) EffectiveFileCaps() (fp, fi Set, eff bool) {
	if !f.UserNamespaceMapped {
		return 0, 0, false
	}
	return f.Permitted, f.Inheritable, f.Effective
}

// 转换过程中可返回的错误。
var (
	// ErrCapOutOfRange 表示能力编号超出本包位宽。
	ErrCapOutOfRange = errors.New("netsec: capability index out of range")
	// ErrPrivilegeEscalation 表示 no_new_privs 下转换结果会超出旧
	// permitted 集合。
	ErrPrivilegeEscalation = errors.New("netsec: new permitted set exceeds old permitted set with no_new_privs")
	// ErrInvariantViolation 表示输入进程状态不满足内核不变量
	// （A ⊆ P、E ⊆ P 等）。
	ErrInvariantViolation = errors.New("netsec: process capability invariant violated")
	// ErrLockedSecureBit 表示尝试清除已锁定的 securebit 或修改被锁
	// 冻结的基础位。
	ErrLockedSecureBit = errors.New("netsec: securebit is locked and cannot be changed")
	// ErrAmbientNotRaisable 表示 ambient 能力不满足提升前置条件。
	ErrAmbientNotRaisable = errors.New("netsec: ambient capability cannot be raised")
)
