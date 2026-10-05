package pager

import "sort"

// PageRequest 描述一次翻页请求。
type PageRequest struct {
	// Sort 排序规则；带游标续查时必须与首页一致，否则报 ErrSortMismatch。
	Sort SortSpec
	// Size 页大小，必须为正。
	Size int
	// Cursor 上一页返回的 NextCursor / PrevCursor；为空表示开启新快照。
	Cursor string
	// Dir 翻页方向，零值为 Forward。
	Dir Direction
}

// Page 是一页结果。
type Page struct {
	// Items 本页记录（快照时刻的副本），按排序规则有序。
	Items []Record
	// NextCursor 正向续查游标；HasNext 为 false 时为空。
	NextCursor string
	// PrevCursor 反向续查游标；HasPrev 为 false 时为空。
	PrevCursor string
	// HasNext 排序方向上是否还有更多行。
	HasNext bool
	// HasPrev 排序反方向上是否还有更多行。
	HasPrev bool
	// SnapshotID 本页所属快照，可用于主动释放。
	SnapshotID string
}

// Paginator 在 Store 上提供快照键集分页。
//
// 工作原理：首页请求时物化一个不可变有序快照；游标记录
// 「快照编号 + 排序规则指纹 + 方向 + 上一页末行/首行的排序键与唯一编号」。
// 续查时在快照内按 (排序键, 唯一编号) 二分定位，不依赖行号，
// 因此翻页期间的增删改不会导致漏行、重行，也不要求游标行仍然存在。
type Paginator struct {
	store *Store
}

// NewPaginator 创建分页器。
func NewPaginator(store *Store) *Paginator {
	return &Paginator{store: store}
}

// Page 取一页。
func (p *Paginator) Page(req PageRequest) (Page, error) {
	if req.Size <= 0 {
		return Page{}, ErrInvalidPageSize
	}
	if len(req.Sort) == 0 {
		return Page{}, ErrNoSortFields
	}

	var (
		snap *Snapshot
		lo   int // 本页在快照中的起始下标（含）
		hi   int // 本页在快照中的结束下标（不含）
	)

	if req.Cursor == "" {
		snap = p.store.newSnapshot(req.Sort)
		lo, hi = windowWithoutCursor(len(snap.entries), req.Size, req.Dir)
	} else {
		cur, err := decodeCursor(req.Cursor)
		if err != nil {
			return Page{}, err
		}
		if cur.Dir != req.Dir {
			return Page{}, ErrDirectionMismatch
		}
		snap = p.store.snapshot(cur.Snapshot)
		if snap == nil {
			return Page{}, ErrSnapshotExpired
		}
		if snap.sortHash != cur.SortHash || cur.SortHash != req.Sort.Hash() {
			return Page{}, ErrSortMismatch
		}
		if len(cur.Keys) != len(req.Sort) {
			return Page{}, ErrCursorMalformed
		}
		lo, hi = windowFromCursor(snap.entries, req.Sort, cur, req.Size, req.Dir)
	}

	items := make([]Record, hi-lo)
	for i := lo; i < hi; i++ {
		items[i-lo] = cloneRecord(snap.entries[i].rec)
	}

	page := Page{
		Items:      items,
		HasNext:    hi < len(snap.entries),
		HasPrev:    lo > 0,
		SnapshotID: snap.id,
	}
	if len(items) > 0 {
		if page.HasNext {
			last := snap.entries[hi-1]
			tok, err := encodeCursor(cursor{
				V: 1, Snapshot: snap.id, SortHash: snap.sortHash,
				Dir: Forward, Keys: last.keys, ID: last.rec.ID,
			})
			if err != nil {
				return Page{}, err
			}
			page.NextCursor = tok
		}
		if page.HasPrev {
			first := snap.entries[lo]
			tok, err := encodeCursor(cursor{
				V: 1, Snapshot: snap.id, SortHash: snap.sortHash,
				Dir: Backward, Keys: first.keys, ID: first.rec.ID,
			})
			if err != nil {
				return Page{}, err
			}
			page.PrevCursor = tok
		}
	}
	return page, nil
}

// windowWithoutCursor 计算首页窗口：正向从头开始，反向取最后一页。
func windowWithoutCursor(n, size int, dir Direction) (lo, hi int) {
	if dir == Backward {
		lo = n - size
		if lo < 0 {
			lo = 0
		}
		return lo, n
	}
	hi = size
	if hi > n {
		hi = n
	}
	return 0, hi
}

// windowFromCursor 在快照内按键集游标二分定位续查窗口。
// 只比较 (排序键, 唯一编号)，与行当前是否存在、行号无关。
func windowFromCursor(entries []entry, spec SortSpec, cur cursor, size int, dir Direction) (lo, hi int) {
	less := func(aKeys []Value, aID string, bKeys []Value, bID string) bool {
		c := spec.compareKeys(aKeys, bKeys)
		if c != 0 {
			return c < 0
		}
		return aID < bID
	}

	if dir == Backward {
		// 第一个 >= 游标键的位置，之前的行严格小于游标键。
		end := sort.Search(len(entries), func(i int) bool {
			return !less(entries[i].keys, entries[i].rec.ID, cur.Keys, cur.ID)
		})
		lo = end - size
		if lo < 0 {
			lo = 0
		}
		return lo, end
	}
	// 第一个 > 游标键的位置。
	start := sort.Search(len(entries), func(i int) bool {
		return less(cur.Keys, cur.ID, entries[i].keys, entries[i].rec.ID)
	})
	hi = start + size
	if hi > len(entries) {
		hi = len(entries)
	}
	return start, hi
}
