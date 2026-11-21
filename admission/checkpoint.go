package admission

import "slices"

// Valid 校验检查点：分片必须完整覆盖 48 位空间，权限区间必须完整且
// 不重叠覆盖页空间，基线内容必须分片对齐且互不重叠。
func (cp Checkpoint) Valid() error {
	if len(cp.Root) == 0 {
		return ErrBadReceipt
	}
	if !validateShards(cp.Shards) {
		return ErrBadShards
	}
	perms := slices.Clone(cp.Permissions)
	slices.SortFunc(perms, func(a, b PermissionRange) int {
		return cmpUint64(a.Pages.Start, b.Pages.Start)
	})
	if len(perms) == 0 || perms[0].Pages.Start != 0 {
		return ErrBadPermissions
	}
	var next uint64
	for _, pr := range perms {
		if !pr.Pages.Valid() || pr.Pages.Start != next {
			return ErrBadPermissions
		}
		next = pr.Pages.End + 1
		if pr.Pages.End == MaxPage {
			next = 0
			break
		}
	}
	if next != 0 {
		return ErrBadPermissions
	}

	base := slices.Clone(cp.Base)
	slices.SortFunc(base, func(a, b BaseContent) int {
		return cmpUint64(a.Pages.Start, b.Pages.Start)
	})
	for i, bc := range base {
		if !bc.Pages.Valid() || !alignedToShards(bc.Pages, cp.Shards) ||
			len(bc.Digest) == 0 {
			return ErrBadReceipt
		}
		if i > 0 && bc.Pages.Start <= base[i-1].Pages.End {
			return ErrBadReceipt
		}
	}
	return nil
}

// baseState 从检查点构造初始映像：未被基线内容覆盖的位置保持无内容状态，
// 但仍携带检查点声明的位置权限。
func (g *graph) baseState() (*intervalMap[pageState], error) {
	perms := slices.Clone(g.in.Checkpoint.Permissions)
	slices.SortFunc(perms, func(a, b PermissionRange) int {
		return cmpUint64(a.Pages.Start, b.Pages.Start)
	})
	base := newIntervalMap(pageState{})
	var next uint64
	for _, pr := range perms {
		if !pr.Pages.Valid() || pr.Pages.Start != next {
			return nil, ErrBadPermissions
		}
		base.overlay(pr.Pages, unattested(pr.Perm))
		if pr.Pages.End == MaxPage {
			next = 0
			break
		}
		next = pr.Pages.End + 1
	}
	if next != 0 {
		return nil, ErrBadPermissions
	}

	for _, bc := range g.in.Checkpoint.Base {
		base.overlay(bc.Pages, pageState{
			digest:   string(bc.Digest),
			trust:    bc.Trust,
			perm:     base.get(bc.Pages.Start).perm,
			attested: bc.Trust == TrustTrusted,
		})
	}
	return base, nil
}

func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
