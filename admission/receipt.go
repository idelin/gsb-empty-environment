package admission

import (
	"bytes"
	"slices"
)

// validateReceipt 做纯结构校验：两类收据都必须绑定发布者、旧根、新根与
// 作用页区间；内容收据必须分片对齐且条目恰好覆盖作用区间；权限收据只能
// 声明收紧子区间。
func validateReceipt(rcpt Receipt, shards []Range) error {
	if len(rcpt.Publisher) == 0 || len(rcpt.OldRoot) == 0 ||
		len(rcpt.NewRoot) == 0 || !rcpt.Pages.Valid() ||
		bytes.Equal(rcpt.OldRoot, rcpt.NewRoot) {
		return ErrBadReceipt
	}
	switch rcpt.Kind {
	case KindContent:
		return validateContent(rcpt, shards)
	case KindPermission:
		return validatePermission(rcpt)
	default:
		return ErrBadReceipt
	}
}

func validateContent(rcpt Receipt, shards []Range) error {
	if !alignedToShards(rcpt.Pages, shards) ||
		len(rcpt.Content) == 0 || len(rcpt.RevokeExec) != 0 {
		return ErrBadReceipt
	}
	entries := slices.Clone(rcpt.Content)
	slices.SortFunc(entries, func(a, b ContentEntry) int {
		return cmpUint64(a.Pages.Start, b.Pages.Start)
	})
	var covered uint64
	for i, entry := range entries {
		if !entry.Pages.Valid() || len(entry.Digest) == 0 ||
			!alignedToShards(entry.Pages, shards) {
			return ErrBadReceipt
		}
		inter, ok := rcpt.Pages.Intersect(entry.Pages)
		if !ok || inter != entry.Pages {
			return ErrBadReceipt
		}
		if i > 0 {
			if entry.Pages.Start <= entries[i-1].Pages.End {
				return ErrBadReceipt
			}
			covered += entries[i-1].Pages.Len()
		}
	}
	covered += entries[len(entries)-1].Pages.Len()
	if covered != rcpt.Pages.Len() {
		return ErrBadReceipt
	}
	return nil
}

func validatePermission(rcpt Receipt) error {
	if len(rcpt.Content) != 0 || len(rcpt.RevokeExec) == 0 {
		return ErrBadReceipt
	}
	revoked := coalesceRanges(rcpt.RevokeExec)
	for _, rv := range revoked {
		if !rv.Valid() {
			return ErrBadReceipt
		}
		inter, ok := rcpt.Pages.Intersect(rv)
		if !ok || inter != rv {
			return ErrBadReceipt
		}
	}
	return nil
}

// applyReceipt 在单条历史内推进状态；调用时该收据已通过结构校验。
// 状态始终按映像位置更新，绝不把别名位置的权限带到另一个位置。
func applyReceipt(st *intervalMap[pageState], rcpt Receipt, trust Trust) {
	switch rcpt.Kind {
	case KindContent:
		for _, entry := range rcpt.Content {
			overlayContent(st, entry, trust)
		}
	case KindPermission:
		for _, rv := range coalesceRanges(rcpt.RevokeExec) {
			revokeExec(st, rv)
		}
	}
}

// overlayContent 写入新内容摘要，但逐段保留每个映像位置原有的权限。
// 权限属于位置而非内容，绝不能用区间起点的权限覆盖整个条目区间。
func overlayContent(st *intervalMap[pageState], entry ContentEntry, trust Trust) {
	// 先在条目两端切分，使条目内部已有的段边界保持稳定。
	startIdx := st.split(entry.Pages.Start)
	if entry.Pages.End < MaxPage {
		st.split(entry.Pages.End + 1)
	}
	for i := startIdx; i < len(st.bounds); i++ {
		if st.bounds[i] > entry.Pages.End {
			break
		}
		page := st.bounds[i]
		st.modifySegment(page, func(ps pageState) pageState {
			return pageState{
				digest:   string(entry.Digest),
				trust:    trust,
				perm:     ps.perm, // 保留该位置原有权限
				attested: trust == TrustTrusted,
			}
		})
	}
	st.compact()
}

// revokeExec 只清除执行位，且按段边界推进而不是逐页遍历。
func revokeExec(st *intervalMap[pageState], rv Range) {
	// 先在区间两端切开，确保修改只影响 rv 覆盖的段而不波及同段邻页。
	startIdx := st.split(rv.Start)
	if rv.End < MaxPage {
		st.split(rv.End + 1)
	}
	for i := startIdx; i < len(st.bounds); i++ {
		if st.bounds[i] > rv.End {
			break
		}
		st.modifySegment(st.bounds[i], func(ps pageState) pageState {
			ps.perm &^= PermExec
			return ps
		})
	}
	st.compact()
}
