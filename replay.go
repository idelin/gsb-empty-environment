package netsec

// ReplayReport 是独立重放的核验结果；全部字段为 true 才算攻击成立。
type ReplayReport struct {
	// Consistent：按 Replacements 重放的伪造消息与 Finding.Forged 逐字节一致。
	Consistent bool
	// Legal：每次替换都符合攻击者能力（未覆盖叶子修改 / 已观察子树复制）。
	Legal bool
	// CoverageIntact：所有签名覆盖路径的子树在伪造前后规范编码相同。
	CoverageIntact bool
	// SignatureOK：伪造消息通过原验证谓词。
	SignatureOK bool
	// TargetChanged：授权目标（接收方或操作）确实改变。
	TargetChanged bool

	Before Target
	After  Target
}

// OK 汇总重放结论。
func (r *ReplayReport) OK() bool {
	return r.Consistent && r.Legal && r.CoverageIntact && r.SignatureOK && r.TargetChanged
}

// Replay 独立重放核验 Finding：不信任搜索过程的任何簿记，
// 从原始消息、观察集合与替换清单重新推导全部事实。
func Replay(original *Node, observations []*Node, f *Finding, verify func(*Node) bool, opts Options) (*ReplayReport, error) {
	opts = opts.withDefaults()
	rep := &ReplayReport{}

	covered, err := CoveredPaths(original, opts.SigPath)
	if err != nil {
		return nil, err
	}
	before, err := AuthTarget(original, opts.ToPath, opts.OpPath)
	if err != nil {
		return nil, err
	}
	rep.Before = before

	// 1. 合法性：逐条检查替换是否属于攻击者能力。
	rep.Legal = true
	for _, r := range f.Replacements {
		switch r.Kind {
		case ModifyLeaf:
			if pathCovered(r.Path, covered) || pathCoversOther(r.Path, covered) {
				rep.Legal = false
				continue
			}
			if !observedAt(observations, r.Path, r.Value, true) {
				rep.Legal = false
			}
		case CopySubtree:
			if !observedAt(observations, r.Path, r.Value, false) {
				rep.Legal = false
			}
		default:
			rep.Legal = false
		}
	}

	// 2. 一致性：独立重放替换并与 Finding.Forged 比对。
	replayed, err := applyReplacements(original, f.Replacements)
	if err != nil {
		return nil, err
	}
	rep.Consistent = Equal(replayed, f.Forged)

	// 3. 覆盖完整性：覆盖路径的子树编码在伪造前后不变。
	rep.CoverageIntact = true
	for _, c := range covered {
		origSub, err1 := original.Get(c)
		forgedSub, err2 := f.Forged.Get(c)
		if err1 != nil || err2 != nil || !Equal(origSub, forgedSub) {
			rep.CoverageIntact = false
			break
		}
	}

	// 4. 验证谓词与最终目标。
	rep.SignatureOK = verify(f.Forged)
	after, err := AuthTarget(f.Forged, opts.ToPath, opts.OpPath)
	if err != nil {
		return nil, err
	}
	rep.After = after
	rep.TargetChanged = after != before

	return rep, nil
}

// observedAt 检查 value 是否出现在某条观察消息的同一路径；
// leafOnly 为 true 时要求观察值也是叶子。
func observedAt(observations []*Node, path string, value *Node, leafOnly bool) bool {
	for _, obs := range observations {
		sub, err := obs.Get(path)
		if err != nil {
			continue
		}
		if leafOnly && (sub.Kind == KindMap || sub.Kind == KindList) {
			continue
		}
		if Equal(sub, value) {
			return true
		}
	}
	return false
}
