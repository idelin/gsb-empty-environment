package netsec

// checkInvariants 核对进程能力集合必须满足的内核不变量：
// A ⊆ P、E ⊆ P、A ⊆ B。
func (p Process) checkInvariants() error {
	if !p.Ambient.IsSubset(p.Permitted) {
		return ErrInvariantViolation
	}
	if !p.Effective.IsSubset(p.Permitted) {
		return ErrInvariantViolation
	}
	if !p.Ambient.IsSubset(p.Bounding) {
		return ErrInvariantViolation
	}
	return nil
}

// transition 是不经不变量校验的纯转换，供 Execve 与测试中的 oracle 复用。
//
// no_new_privs 置位时文件特权（文件能力与 setuid/setgid 位）全部忽略；
// 特权文件先清空 ambient；随后按
//
//	P' = (I ∩ F_i) ∪ (F_p ∩ B) ∪ A'
//
// 计算新 permitted，effective 标记决定 E' 取 P' 还是 A'。
func transition(p Process, f File) Process {
	fp, fi, fEff := f.EffectiveFileCaps()

	// no_new_privs：文件不再贡献任何特权，等价于无文件能力、非特权文件。
	if p.NoNewPrivs {
		fp, fi, fEff = 0, 0, false
	}

	// 特权文件（且未被 no_new_privs 压制）在 exec 时清空 ambient。
	ambient := p.Ambient
	if !p.NoNewPrivs && f.Privileged() {
		ambient = 0
	}

	permitted := p.Inheritable.Intersect(fi).
		Union(fp.Intersect(p.Bounding)).
		Union(ambient)

	effective := ambient
	if fEff {
		effective = permitted
	}

	return Process{
		Permitted:   permitted,
		Inheritable: p.Inheritable,
		Ambient:     ambient,
		Bounding:    p.Bounding,
		Effective:   effective,
		SecureBits:  p.SecureBits,
		NoNewPrivs:  p.NoNewPrivs,
	}
}

// Execve 模拟执行文件 f 后的能力转换，返回收窄后的新进程状态。
//
// 输入状态违反内核不变量时返回 ErrInvariantViolation；no_new_privs 下
// 若新 permitted 超出旧 permitted，返回 ErrPrivilegeEscalation。
func (p Process) Execve(f File) (Process, error) {
	if err := p.checkInvariants(); err != nil {
		return Process{}, err
	}

	next := transition(p, f)

	// 禁止提权标记：新 P 不得超出旧 P。正常内核语义下该条件恒成立，
	// 这里作为防御性显式校验。
	if p.NoNewPrivs && !next.Permitted.IsSubset(p.Permitted) {
		return Process{}, ErrPrivilegeEscalation
	}
	return next, nil
}
