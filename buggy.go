package netsec

import (
	"errors"
	"fmt"
)

// closePendingReview 是待审的“先回调再清空”版本。
//
// 与修订版的差异（即本次审查目标）：
//  1. 进入 Closing 后只快照句柄用于遍历，不从对象上清空，也不维护冻结列表；
//  2. 回调期间 MoveTo/Call 的所有权校验仍能通过（见 host.go 中对应分支）；
//  3. Release 不做去重，同一句柄可能被释放两次。
//
// 后果：回调中把句柄 MoveTo 到另一个 Open 对象并 Close 之，句柄被 Release
// 一次；回到本对象的释放循环后，快照中仍持有该句柄，被再次 Release。
func (h *Host) closePendingReview(o *Object) error {
	h.mu.Lock()
	switch o.state {
	case StateClosed:
		err := o.closeErr
		h.mu.Unlock()
		return err
	case StateClosing:
		h.mu.Unlock()
		return ErrCloseInProgress
	}
	o.state = StateClosing
	// BUG：只快照、不清空；o.handles 在回调期间仍然有效。
	snapshot := append([]Handle(nil), o.handles...)
	h.mu.Unlock()

	h.push(fmt.Sprintf("Close(%s) [待审:先回调再清空]", o.name))
	var errs []error
	for _, hd := range snapshot {
		h.fireBeforeRelease(o, hd)
		if err := h.releaseAlways(o, hd); err != nil {
			errs = append(errs, fmt.Errorf("release handle #%d: %w", hd.ID, err))
		}
	}
	agg := errors.Join(errs...)

	h.mu.Lock()
	o.handles = nil // 释放全部结束后才清空
	o.gen++
	o.state = StateClosed
	o.closeErr = agg
	h.mu.Unlock()

	h.fireOnClosed(o)
	h.pop()
	return agg
}

// releaseAlways 待审版 Release：不做去重，重复 Release 时记录违规。
func (h *Host) releaseAlways(o *Object, hd Handle) error {
	h.mu.Lock()
	h.releases[hd.ID]++
	n := h.releases[hd.ID]
	h.mu.Unlock()
	h.push(fmt.Sprintf("Release(h%d)", hd.ID))
	defer h.pop()
	if n > 1 {
		h.recordViolation(ViolationDoubleRelease,
			fmt.Sprintf("句柄 h%d 被第 %d 次 Release", hd.ID, n))
	}
	if h.releaseFn == nil {
		return nil
	}
	return h.releaseFn(hd)
}
