package rootslot

import (
	"bytes"
	"errors"
	"fmt"
)

var (
	// ErrBothSlotsCorrupt 两个槽都损坏（非空但校验失败），拒绝打开。
	ErrBothSlotsCorrupt = errors.New("rootslot: both metadata slots are corrupt")
	// ErrSlotConflict 两个槽序号相同但内容不同，属于必须人工诊断的冲突。
	ErrSlotConflict = errors.New("rootslot: slots share sequence number but differ in content")
	// ErrSequenceExhausted 序号溢出策略：绝不回绕，耗尽即拒绝提交。
	ErrSequenceExhausted = errors.New("rootslot: sequence number exhausted, refusing to wrap")
	// ErrReplayDetected 目标槽已持有 >= 本次序号的记录，疑似旧槽重放。
	ErrReplayDetected = errors.New("rootslot: target slot already holds a newer or equal sequence (replay)")
	// ErrPayloadTooLarge 根描述符超过槽容量。
	ErrPayloadTooLarge = errors.New("rootslot: root payload exceeds slot capacity")
)

// maxSeq 是序号上限；达到后拒绝继续提交而不是回绕到 0。
const maxSeq = ^uint64(0)

// Store 通过两个带序号和校验和的元数据槽原子发布根指针。
type Store struct {
	be        Backend
	validator Validator

	active int // 当前生效槽：0/1；-1 表示空仓库
	seq    uint64
	root   []byte
	diags  []string
}

// Open 打开调用方指定目录下的存储（真实文件后端）。
func Open(dir string, v Validator) (*Store, error) {
	be, err := NewFileBackend(dir)
	if err != nil {
		return nil, err
	}
	s, err := OpenBackend(be, v)
	if err != nil {
		be.Close()
		return nil, err
	}
	return s, nil
}

// OpenBackend 在任意后端上打开存储，执行槽选择：
//   - 两槽都损坏：拒绝打开；
//   - 仅一槽有效：使用该槽（另一槽的损坏记入诊断）；
//   - 两槽都有效：取序号较大者；序号相同内容不同报冲突；
//     序号差 != 1 说明中间有提交丢失，记入诊断（不拒绝）。
func OpenBackend(be Backend, v Validator) (*Store, error) {
	s := &Store{be: be, validator: v, active: -1}
	var contents [numSlots]slotContent
	var states [numSlots]slotState
	buf := make([]byte, SlotSize)
	for i := 0; i < numSlots; i++ {
		if _, err := be.ReadAt(buf, int64(i*SlotSize)); err != nil {
			return nil, fmt.Errorf("rootslot: read slot %d: %w", i, err)
		}
		contents[i], states[i] = decodeSlot(buf)
	}

	switch {
	case states[0] == slotCorrupt && states[1] == slotCorrupt:
		return nil, ErrBothSlotsCorrupt
	case states[0] == slotValid && states[1] == slotValid:
		a, b := contents[0], contents[1]
		if a.seq == b.seq {
			if !bytes.Equal(a.payload, b.payload) {
				return nil, fmt.Errorf("%w (seq=%d)", ErrSlotConflict, a.seq)
			}
			s.useSlot(0, a)
			break
		}
		hi, lo := 0, 1
		if b.seq > a.seq {
			hi, lo = 1, 0
		}
		if contents[hi].seq-contents[lo].seq != 1 {
			s.diags = append(s.diags, fmt.Sprintf(
				"sequence gap between slots: %d and %d (some commit was lost)",
				contents[lo].seq, contents[hi].seq))
		}
		s.useSlot(hi, contents[hi])
	case states[0] == slotValid:
		s.maybeCorruptDiag(states[1], 1)
		s.useSlot(0, contents[0])
	case states[1] == slotValid:
		s.maybeCorruptDiag(states[0], 0)
		s.useSlot(1, contents[1])
	default:
		// 其余组合（empty/corrupt）：仓库视为空；损坏槽记入诊断。
		s.maybeCorruptDiag(states[0], 0)
		s.maybeCorruptDiag(states[1], 1)
	}
	return s, nil
}

func (s *Store) maybeCorruptDiag(st slotState, idx int) {
	if st == slotCorrupt {
		s.diags = append(s.diags, fmt.Sprintf("slot %d is corrupt; ignored (never guessed)", idx))
	}
}

func (s *Store) useSlot(idx int, c slotContent) {
	s.active = idx
	s.seq = c.seq
	s.root = append([]byte(nil), c.payload...)
}

// Root 返回当前根描述符；空仓库返回 nil。
func (s *Store) Root() []byte { return append([]byte(nil), s.root...) }

// Seq 返回当前根对应的序号；空仓库为 0。
func (s *Store) Seq() uint64 { return s.seq }

// Diagnostics 返回打开/提交过程中记录的非致命异常（槽损坏、序号空洞等）。
func (s *Store) Diagnostics() []string { return append([]string(nil), s.diags...) }

// Commit 原子发布新根：
//  1. 先由 Validator 独立检查新根的可达闭包，失败则不发布；
//  2. 序号 +1，溢出即拒绝（绝不回绕）；
//  3. 目标槽若已持有 >= 新序号的记录，判定旧槽重放并拒绝；
//  4. 整槽一次 WriteAt 写入备用槽，随后 Sync；
//     Sync 返回前崩溃，恢复结果只能是旧根或新根，不会是中间态。
func (s *Store) Commit(root []byte) error {
	if len(root) > MaxPayload {
		return ErrPayloadTooLarge
	}
	if s.validator != nil {
		if err := s.validator.ValidateReachable(root); err != nil {
			return fmt.Errorf("rootslot: reachable-closure validation failed, slot not published: %w", err)
		}
	}
	if s.seq == maxSeq {
		return ErrSequenceExhausted
	}
	newSeq := s.seq + 1

	target := 0
	if s.active == 0 {
		target = 1
	}
	// 旧槽重放检测：目标槽里不允许躺着 >= newSeq 的有效记录。
	buf := make([]byte, SlotSize)
	if _, err := s.be.ReadAt(buf, int64(target*SlotSize)); err != nil {
		return fmt.Errorf("rootslot: read target slot: %w", err)
	}
	if c, st := decodeSlot(buf); st == slotValid && c.seq >= newSeq {
		return fmt.Errorf("%w: slot %d holds seq=%d, want to write seq=%d",
			ErrReplayDetected, target, c.seq, newSeq)
	}

	if _, err := s.be.WriteAt(encodeSlot(newSeq, root), int64(target*SlotSize)); err != nil {
		return fmt.Errorf("rootslot: write slot %d: %w", target, err)
	}
	if err := s.be.Sync(); err != nil {
		return fmt.Errorf("rootslot: sync slot %d: %w", target, err)
	}
	s.active = target
	s.seq = newSeq
	s.root = append([]byte(nil), root...)
	return nil
}

// Verify 用 Validator 独立复查当前根的可达闭包。
func (s *Store) Verify() error {
	if s.validator == nil || s.active == -1 {
		return nil
	}
	return s.validator.ValidateReachable(s.root)
}

// Close 关闭底层后端。
func (s *Store) Close() error { return s.be.Close() }
