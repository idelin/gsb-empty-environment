package cursorpage

import (
	"context"
	"errors"
	"sort"
)

const (
	modeCursor    = "cursor"
	modeBacktrack = "backtrack"

	defaultPageSize = 100
	defaultDedupCap = 100_000
)

// Reader 是支持断点续传的游标翻页读取器。
//
// 交付语义（在模拟崩溃注入点的前提下可验证）：
//   - 每条存活记录至少交付一次（at-least-once）；
//   - 同一个 (Key, Version) 在整个读取生命周期内最多交付一次；
//   - 崩溃只发生在“请求远端之前”或“断点原子落盘之后”这两个安全点，
//     因此已落盘的版本绝不会被重复交付。若 Deliver 回调自身在业务提交后、
//     断点落盘前失败，则属于普通 at-least-once 边界，需要下游按
//     (Key, Version) 幂等兜底。
type Reader struct {
	cfg Config
}

func NewReader(cfg Config) *Reader {
	if cfg.PageSize <= 0 {
		cfg.PageSize = defaultPageSize
	}
	if cfg.DedupCap <= 0 {
		cfg.DedupCap = defaultDedupCap
	}
	if cfg.BacktrackOverlap < 0 {
		cfg.BacktrackOverlap = 0
	}
	return &Reader{cfg: cfg}
}

// Run 执行采集循环，直到回溯扫描确认没有更多数据为止。
// deliver 对每条首次见到的 (Key, Version) 同步调用一次。
// 返回注入的 ErrCrash 时，断点文件已处于一致状态，重建 Reader 后可继续。
func (r *Reader) Run(ctx context.Context, deliver func(Record) error) error {
	cp, ok, err := r.cfg.Store.Load(ctx)
	if err != nil {
		return err
	}
	if !ok {
		cp = newCheckpoint()
		if r.cfg.BacktrackOnly {
			next, err := r.switchToBacktrack(ctx, cp)
			if err != nil {
				return err
			}
			cp = *next
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var next *Checkpoint
		switch cp.Mode {
		case modeCursor:
			next, err = r.runCursor(ctx, cp, deliver)
		case modeBacktrack:
			next, err = r.runBacktrack(ctx, cp, deliver)
		default:
			return errors.New("cursorpage: unknown checkpoint mode " + cp.Mode)
		}
		if err != nil {
			return err
		}
		if next == nil {
			// 回溯扫描排空，采集完成。
			return nil
		}
		cp = *next
	}
}

func newCheckpoint() Checkpoint {
	return Checkpoint{
		Mode:      modeCursor,
		Delivered: map[string]int64{},
	}
}

// runCursor 执行游标模式，返回下一轮要使用的状态。
// 返回 (nil, nil) 之外的情形：
//   - 游标自然耗尽：切换到回溯模式做收尾扫描；
//   - 游标过期：同样切换到回溯模式；
//   - 注入崩溃或其它错误：状态已落盘，错误向上返回。
func (r *Reader) runCursor(ctx context.Context, cp Checkpoint, deliver func(Record) error) (*Checkpoint, error) {
	if hook := r.cfg.Hooks.BeforeFetch; hook != nil {
		if err := hook(modeCursor); err != nil {
			return nil, err
		}
	}
	page, err := r.cfg.Remote.ListByCursor(ctx, cp.Cursor, r.cfg.PageSize)
	if err != nil {
		if errors.Is(err, ErrCursorExpired) {
			return r.switchToBacktrack(ctx, cp)
		}
		return nil, err
	}

	// 记录当前页服务端视角大小，供恢复时收缩本页偏移。
	cp.CursorPageCount = len(page.Records)
	if cp.PageDelivered > len(page.Records) {
		// 本页在崩溃期间被服务端收缩，偏移夹到页尾。
		cp.PageDelivered = len(page.Records)
	}
	if err := r.persist(ctx, &cp); err != nil {
		return nil, err
	}

	for idx := cp.PageDelivered; idx < len(page.Records); idx++ {
		rec := page.Records[idx]
		if _, err := r.deliverIfNew(ctx, &cp, rec, deliver); err != nil {
			return nil, err
		}
		// 无论该条是否真正发出（可能被去重），本页位置都要推进，
		// 并随断点原子落盘，保证恢复后不会重放本页。
		cp.PageDelivered = idx + 1
		if err := r.persist(ctx, &cp); err != nil {
			return nil, err
		}
	}

	if hook := r.cfg.Hooks.AfterPage; hook != nil {
		hook(modeCursor)
	}

	if page.NextCursor == "" {
		// 游标自然耗尽：插入位置早于当前游标、游标翻不到的数据，
		// 交给回溯窗口兜底扫描。
		return r.switchToBacktrack(ctx, cp)
	}

	cp.Cursor = page.NextCursor
	cp.PageDelivered = 0
	cp.CursorPageCount = 0
	if err := r.persist(ctx, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// runBacktrack 按 (UpdatedAt, Version) 升序做回溯窗口扫描。
// 扫描位置完全保存在断点里，服务端无状态，重复请求只会拿到同一批数据，
// 配合去重表保证幂等。
func (r *Reader) runBacktrack(ctx context.Context, cp Checkpoint, deliver func(Record) error) (*Checkpoint, error) {
	if hook := r.cfg.Hooks.BeforeFetch; hook != nil {
		if err := hook(modeBacktrack); err != nil {
			return nil, err
		}
	}
	page, err := r.cfg.Remote.ListByUpdatedAt(ctx, cp.BTUpdatedAt, cp.BTVersion, r.cfg.PageSize)
	if err != nil {
		return nil, err
	}

	if page.End || len(page.Records) == 0 {
		// 本趟扫描排空。只有扫描期间高水位被推进过（说明有并发新版本
		// 落在本趟游标之前），才再开一趟重叠窗口扫描；重叠窗口保证新锚点
		// 相对旧锚点单调不后移，去重表保证重扫幂等。
		if cp.HighWaterMark <= cp.BTScanStartHWM {
			return nil, nil
		}
		anchor := cp.HighWaterMark - r.cfg.BacktrackOverlap
		if anchor < cp.BacktrackAnchor {
			anchor = cp.BacktrackAnchor
		}
		cp.BacktrackAnchor = anchor
		cp.BTUpdatedAt = anchor
		cp.BTVersion = 0
		cp.BTScanStartHWM = cp.HighWaterMark
		if err := r.persist(ctx, &cp); err != nil {
			return nil, err
		}
		return &cp, nil
	}

	for _, rec := range page.Records {
		if _, err := r.deliverIfNew(ctx, &cp, rec, deliver); err != nil {
			return nil, err
		}
		// 推进 (UpdatedAt, Version) 扫描位置并原子落盘。
		cp.BTUpdatedAt = rec.UpdatedAt
		cp.BTVersion = rec.Version
		if err := r.persist(ctx, &cp); err != nil {
			return nil, err
		}
	}

	if hook := r.cfg.Hooks.AfterPage; hook != nil {
		hook(modeBacktrack)
	}
	return &cp, nil
}

// switchToBacktrack 从游标模式切换到回溯模式，锚点为高水位减去回溯窗口。
func (r *Reader) switchToBacktrack(ctx context.Context, cp Checkpoint) (*Checkpoint, error) {
	anchor := cp.HighWaterMark - r.cfg.BacktrackOverlap
	if anchor < 0 {
		anchor = 0
	}
	cp.Mode = modeBacktrack
	cp.BacktrackAnchor = anchor
	// 从锚点开始，版本位置归零表示锚点时刻的第一条版本。
	cp.BTUpdatedAt = anchor
	cp.BTVersion = 0
	cp.BTScanStartHWM = cp.HighWaterMark
	if err := r.persist(ctx, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// deliverIfNew 按 (Key, Version) 去重后交付记录，并同步推进所有续传坐标。
// 返回 emitted 表示是否真正调用了 deliver。
func (r *Reader) deliverIfNew(ctx context.Context, cp *Checkpoint, rec Record, deliver func(Record) error) (bool, error) {
	if rec.UpdatedAt > cp.HighWaterMark {
		cp.HighWaterMark = rec.UpdatedAt
	}

	if last, ok := cp.Delivered[rec.Key]; ok && rec.Version <= last {
		// 旧版本（含同一版本）重复出现，跳过。
		return false, nil
	}

	if err := deliver(rec); err != nil {
		return false, err
	}

	cp.Delivered[rec.Key] = rec.Version
	cp.LastKey = rec.Key
	cp.LastVersion = rec.Version
	r.evictDedup(cp)

	// 交付与断点推进在同一个原子落盘单元里提交：
	// 崩溃后要么这条没交付（重放），要么已交付且已记录（不重放）。
	if err := r.persist(ctx, cp); err != nil {
		return false, err
	}
	return true, nil
}

// evictDedup 在去重表超过上限时淘汰版本号最小（最久未更新）的键，
// 把内存占用限制在 DedupCap 以内。
func (r *Reader) evictDedup(cp *Checkpoint) {
	if len(cp.Delivered) <= r.cfg.DedupCap {
		return
	}
	type kv struct {
		key     string
		version int64
	}
	entries := make([]kv, 0, len(cp.Delivered))
	for k, v := range cp.Delivered {
		entries = append(entries, kv{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].version != entries[j].version {
			return entries[i].version < entries[j].version
		}
		return entries[i].key < entries[j].key
	})
	excess := len(entries) - r.cfg.DedupCap
	for i := 0; i < excess; i++ {
		delete(cp.Delivered, entries[i].key)
	}
}

func (r *Reader) persist(ctx context.Context, cp *Checkpoint) error {
	if err := r.cfg.Store.Save(ctx, *cp); err != nil {
		return err
	}
	if hook := r.cfg.Hooks.AfterCheckpoint; hook != nil {
		if err := hook(*cp); err != nil {
			return err
		}
	}
	return nil
}
