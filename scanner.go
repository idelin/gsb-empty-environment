package snapscan

import (
	"errors"
	"sync"
)

// Cursor 是可恢复的分页游标。它包含两部分：
//   - Snapshot：本次扫描固定使用的数据快照；
//   - LastScannedKey：最后*扫描*到的键，而不是最后*交付*的键。
//
// 使用最后扫描键的意义在于：被权限拒绝的键同样会推进游标，
// 不会在后续页被重复扫描，也不会因为重新授权而补发——
// 一次扫描中每个键只被判定一次，判定时刻即交付时刻。
type Cursor struct {
	Snapshot       Snapshot
	LastScannedKey string
}

// Scanner 在固定数据快照上分页扫描，并在每次交付时按最新权限过滤。
type Scanner struct {
	store *Store
	perms *PermStore

	// MaxScanPerPage 限制单次 NextPage 最多扫描的键数（含被拒绝的），
	// 保证大量连续拒绝时单次调用工作量有界。<=0 表示不限制。
	MaxScanPerPage int

	mu        sync.Mutex
	delivered []Record // 追加式交付日志：已交付项永不撤回
	skipped   []string // 审计日志：被扫描但因当时无权限而跳过的键
}

// NewScanner 创建一个基于 store 与动态权限 perms 的扫描器。
func NewScanner(store *Store, perms *PermStore) *Scanner {
	return &Scanner{store: store, perms: perms}
}

// NextPage 交付至多 pageSize 条当前被授权的数据。
//
// cur 为 nil 时固定一个新的数据快照并从头开始；否则沿 cur 中的快照
// 与扫描位置继续。返回的 next 为 nil 表示快照已扫描完毕。
//
// 注意：若返回的 page 为空但 next 非 nil，说明本次扫描预算被
// 无权限的键耗尽，调用方应携带 next 继续取下一页。
func (s *Scanner) NextPage(cur *Cursor, pageSize int) (page []Record, next *Cursor, err error) {
	if pageSize <= 0 {
		return nil, nil, errors.New("snapscan: pageSize must be positive")
	}
	var c Cursor
	if cur == nil {
		c.Snapshot = s.store.Pin()
	} else {
		c = *cur
	}

	scanned := 0
	exhausted := false
	for len(page) < pageSize && !exhausted {
		if s.MaxScanPerPage > 0 && scanned >= s.MaxScanPerPage {
			break
		}
		batchSize := pageSize - len(page)
		if s.MaxScanPerPage > 0 && batchSize > s.MaxScanPerPage-scanned {
			batchSize = s.MaxScanPerPage - scanned
		}
		batch := s.store.ScanAfter(c.Snapshot, c.LastScannedKey, batchSize)
		if len(batch) == 0 {
			exhausted = true
			break
		}
		for _, rec := range batch {
			// 无论是否交付，游标都推进到该扫描键。
			c.LastScannedKey = rec.Key
			scanned++
			if s.perms.Allow(rec.Key) {
				page = append(page, rec)
			} else {
				s.mu.Lock()
				s.skipped = append(s.skipped, rec.Key)
				s.mu.Unlock()
			}
		}
	}

	if len(page) > 0 {
		s.mu.Lock()
		s.delivered = append(s.delivered, page...)
		s.mu.Unlock()
	}
	if exhausted {
		return page, nil, nil
	}
	next = &c
	return page, next, nil
}

// Delivered 返回迄今为止的全部交付记录（追加式，撤权不会移除）。
func (s *Scanner) Delivered() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.delivered))
	copy(out, s.delivered)
	return out
}

// Skipped 返回因扫描时刻无权限而被跳过的键的审计日志（按扫描顺序）。
func (s *Scanner) Skipped() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.skipped))
	copy(out, s.skipped)
	return out
}
