package auditjoin

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"
)

// Ingest 接收一个审计片段，重复片段幂等丢弃，冲突片段返回 ErrConflict。
func (a *Assembler) Ingest(ctx context.Context, f Fragment) error {
	if err := validateFragment(f); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrAssemblerClosed
	}

	now := a.now()
	sk := streamKey{Node: f.NodeID, Gen: f.Generation}
	ring := a.rings[sk]
	ck := compositeKey{
		Key:   EventKey{NodeID: f.NodeID, Generation: f.Generation, Seq: f.Seq},
		Cycle: ring.Cycle,
	}
	entry, ok := a.open[ck]
	if !ok {
		rec, found, err := a.store.Get(ctx, ck.Key, ck.Cycle)
		if err != nil {
			return err
		}
		if found {
			entry = rebuildEntry(rec)
		} else {
			entry = &openEntry{
				Key:      ck.Key,
				Cycle:    ck.Cycle,
				Frags:    make(map[FragmentID]Fragment),
				Deadline: a.deadline(now),
			}
		}
		a.open[ck] = entry
	}

	// 已发布且超过补正窗口的事件（含已完成事件）一律拒绝迟到片段，
	// 该判定必须先于重复片段判重。
	if entry.PublishedVersion > 0 && a.cfg.CorrectionWindow > 0 &&
		now.Sub(entry.PublishedAt) > a.cfg.CorrectionWindow {
		a.stats.SealedRejections++
		if perr := a.persistLocked(); perr != nil {
			return perr
		}
		return ErrEventSealed
	}

	if existing, dup := entry.Frags[f.FragmentID]; dup {
		if bytes.Equal(existing.Payload, f.Payload) {
			a.stats.DuplicateFragments++
			return a.persistLocked()
		}
		a.stats.Conflicts++
		if perr := a.persistLocked(); perr != nil {
			return perr
		}
		return ErrConflict
	}

	// 仅在确认是新片段后更新回绕环，避免重复/乱序片段污染周期计数。
	if a.cfg.WrapModulus > 0 && ring.InitialRawSet && isWraparound(ring.LastRaw, f.Seq, a.cfg.WrapModulus) {
		ring.Cycle++
	}
	ring.LastRaw = f.Seq
	ring.InitialRawSet = true
	a.rings[sk] = ring
	ck.Cycle = ring.Cycle

	if entry.Frags == nil {
		entry.Frags = make(map[FragmentID]Fragment)
	}
	entry.Frags[f.FragmentID] = f
	entry.Order = append(entry.Order, f.FragmentID)
	if f.ExpectedFragments > 0 {
		entry.ExpectedFragments = f.ExpectedFragments
	}
	entry.DirtySincePublish = entry.PublishedVersion > 0
	a.stats.Ingested++

	switch {
	case entry.ExpectedFragments > 0 && len(entry.Frags) >= entry.ExpectedFragments:
		if _, err := a.publishLocked(ctx, entry, true, now, "complete"); err != nil {
			return err
		}
	case a.watermarkPassedLocked(sk, entry.Cycle, entry.Key.Seq):
		if _, err := a.publishLocked(ctx, entry, true, now, "watermark"); err != nil {
			return err
		}
	}
	return a.persistLocked()
}

// ObserveWatermark 上报某分片已连续处理到的事件序号原始值。
// 所有必需分片的水位线均越过某事件后，该事件立即完整发布。
func (a *Assembler) ObserveWatermark(_ context.Context, shard ShardID, raw uint64) error {
	if shard == "" {
		return fmt.Errorf("%w: empty shard id", ErrInvalidFragment)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrAssemblerClosed
	}
	a.watermarks[shard] = shardWatermark{Raw: raw}
	now := a.now()
	if _, err := a.sweepLocked(context.Background(), now, true); err != nil {
		return err
	}
	return a.persistLocked()
}

// Sweep 执行一轮维护：封板过期条目、超时强制发布、水位线完整发布。
// now 传零值时使用配置的时钟。
func (a *Assembler) Sweep(now time.Time) (SweepResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return SweepResult{Stats: a.stats}, ErrAssemblerClosed
	}
	if now.IsZero() {
		now = a.now()
	}
	return a.sweepLocked(context.Background(), now, false)
}

func (a *Assembler) sweepLocked(ctx context.Context, now time.Time, watermarkOnly bool) (SweepResult, error) {
	res := SweepResult{}
	mutated := false
	var firstErr error

	keys := make([]compositeKey, 0, len(a.open))
	for ck := range a.open {
		keys = append(keys, ck)
	}
	sort.Slice(keys, func(i, j int) bool {
		return compositeKeyString(keys[i]) < compositeKeyString(keys[j])
	})

	for _, ck := range keys {
		entry := a.open[ck]
		stream := streamKey{Node: ck.Key.NodeID, Gen: ck.Key.Generation}

		if !watermarkOnly && entry.PublishedVersion > 0 && a.cfg.CorrectionWindow > 0 &&
			now.Sub(entry.PublishedAt) > a.cfg.CorrectionWindow {
			delete(a.open, ck)
			a.stats.SealedRejections++
			res.Sealed = append(res.Sealed, ck.Key)
			mutated = true
			continue
		}

		if watermarkOnly {
			// 未发布事件按水位线完整首发；已超时发布的半成品在水位线越过时补正为完整。
			if a.watermarkPassedLocked(stream, entry.Cycle, ck.Key.Seq) &&
				(entry.PublishedVersion == 0 || !entry.Complete) {
				alreadyPublished := entry.PublishedVersion > 0
				if _, err := a.publishLocked(ctx, entry, true, now, "watermark"); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				if alreadyPublished {
					res.Corrected = append(res.Corrected, ck.Key)
				} else {
					res.Published = append(res.Published, ck.Key)
				}
				mutated = true
			}
			continue
		}

		if entry.PublishedVersion == 0 && !entry.Deadline.IsZero() && now.After(entry.Deadline) {
			if _, err := a.publishLocked(ctx, entry, false, now, "timeout"); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			res.Published = append(res.Published, ck.Key)
			mutated = true
			continue
		}

		if entry.PublishedVersion == 0 && a.watermarkPassedLocked(stream, entry.Cycle, ck.Key.Seq) {
			if _, err := a.publishLocked(ctx, entry, true, now, "watermark"); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			res.Published = append(res.Published, ck.Key)
			mutated = true
		}
	}

	if mutated {
		if err := a.persistLocked(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	res.Stats = a.stats
	return res, firstErr
}

// watermarkPassedLocked 判断某（代次、回绕周期、原始序号）事件是否已被全部必需分片的水位线越过。
func (a *Assembler) watermarkPassedLocked(stream streamKey, cycle, seq uint64) bool {
	ring, ok := a.rings[stream]
	if !ok {
		return false
	}
	if len(a.required) > 0 {
		for sid := range a.required {
			wm, seen := a.watermarks[sid]
			if !seen || !absGE(wm.Raw, ring, cycle, seq) {
				return false
			}
		}
		return true
	}
	if len(a.watermarks) == 0 {
		return false
	}
	for _, wm := range a.watermarks {
		if !absGE(wm.Raw, ring, cycle, seq) {
			return false
		}
	}
	return true
}

// absGE 把分片上报的原始水位线按当前流的回绕信息折算后，与 (cycle,seq) 比较。
// 水位线只携带原始序号，这里保守地把它折算到流当前所在周期：
// 周期不同直接比较周期；同周期内比较原始序号。
func absGE(wmRaw uint64, ring ringState, cycle, seq uint64) bool {
	if ring.Cycle != cycle {
		return ring.Cycle > cycle
	}
	return wmRaw >= seq
}

func (a *Assembler) backgroundSweep() {
	defer close(a.done)
	t := time.NewTicker(a.cfg.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			// 抢锁前再次检查停止信号，避免 Close 持锁等待本 goroutine 时自死锁。
			select {
			case <-a.stop:
				return
			default:
			}
			a.mu.Lock()
			closed := a.closed
			if !closed {
				_, _ = a.sweepLocked(context.Background(), a.now(), false)
				_ = a.persistLocked()
			}
			a.mu.Unlock()
			if closed {
				return
			}
		}
	}
}
