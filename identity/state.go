package identity

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

type frameKind uint8

const (
	enterFrame frameKind = iota
	borrowFrame
	snapshotFrame
)

type frame struct {
	token    Token
	kind     frameKind
	borrowID int
	thread   int32
}

type handleKey struct {
	slot int
	gen  int64
}

type slotState struct {
	token Token
	gen   int64
	valid bool
}

type kernelState struct {
	executed  uint64
	active    map[int32][]frame
	suspended map[int32][]frame
	slots     map[int]slotState
	consumed  map[handleKey]struct{}
	fp        string
	hash      uint64
}

func freshState() *kernelState {
	s := &kernelState{
		active:    map[int32][]frame{},
		suspended: map[int32][]frame{},
		slots:     map[int]slotState{},
		consumed:  map[handleKey]struct{}{},
	}
	s.hash = stateHash(s)
	return s
}

func cloneFrames(src []frame) []frame {
	if len(src) == 0 {
		return nil
	}
	dst := make([]frame, len(src))
	copy(dst, src)
	return dst
}

func cloneState(s *kernelState) *kernelState {
	n := &kernelState{
		executed:  s.executed,
		active:    make(map[int32][]frame, len(s.active)),
		suspended: make(map[int32][]frame, len(s.suspended)),
		slots:     make(map[int]slotState, len(s.slots)),
		consumed:  make(map[handleKey]struct{}, len(s.consumed)),
	}
	for k, v := range s.active {
		n.active[k] = cloneFrames(v)
	}
	for k, v := range s.suspended {
		n.suspended[k] = cloneFrames(v)
	}
	for k, v := range s.slots {
		n.slots[k] = v
	}
	for k := range s.consumed {
		n.consumed[k] = struct{}{}
	}
	return n
}

func validate(in Input) error {
	if len(in.Events) == 0 {
		return errNoEvents
	}
	if len(in.Events) > maxEvents {
		return errTooManyEvents
	}
	seenID := map[int]struct{}{}
	borrowDefs := map[int]struct{}{}
	for _, ev := range in.Events {
		if _, dup := seenID[ev.ID]; dup {
			return fmt.Errorf("identity: duplicate event id %d", ev.ID)
		}
		seenID[ev.ID] = struct{}{}
		if ev.Kind == Borrow {
			if ev.BorrowID >= 0 {
				if _, dup := borrowDefs[ev.BorrowID]; dup {
					return fmt.Errorf("identity: %w %d", errors.New(ReasonDuplicateBorrow), ev.BorrowID)
				}
				borrowDefs[ev.BorrowID] = struct{}{}
			}
		}
	}
	chainExitDone := map[int]bool{}
	for _, ev := range in.Events {
		if ev.Kind == Exit {
			if chainExitDone[ev.Chain] {
				return fmt.Errorf("identity: chain %d has more than one exit event", ev.Chain)
			}
			chainExitDone[ev.Chain] = true
		}
	}
	for _, ev := range in.Events {
		for _, dep := range ev.After {
			if _, ok := seenID[dep]; !ok {
				return fmt.Errorf("identity: event %d references unknown predecessor %d", ev.ID, dep)
			}
		}
	}
	return nil
}

func buildPredecessors(in Input, index map[int]int) [][]int {
	preds := make([][]int, len(in.Events))
	prevInChain := map[int]int{}
	for i, ev := range in.Events {
		if p, ok := prevInChain[ev.Chain]; ok {
			preds[i] = append(preds[i], p)
		}
		prevInChain[ev.Chain] = i
	}
	for i, ev := range in.Events {
		for _, dep := range ev.After {
			preds[i] = append(preds[i], index[dep])
		}
	}
	return preds
}

func ready(executed uint64, preds []int, _ map[int]int) bool {
	for _, p := range preds {
		if executed&(uint64(1)<<uint(p)) == 0 {
			return false
		}
	}
	return true
}

func (s *kernelState) activeStack(thread int32) []frame {
	return s.active[thread]
}

func effectiveToken(stack []frame) (Token, bool) {
	if len(stack) == 0 {
		return Token{}, false
	}
	return stack[len(stack)-1].token, true
}

func readOutcome(stack []frame, policy Policy) Outcome {
	token, ok := effectiveToken(stack)
	if !ok || token.Tenant != policy.Tenant || token.Generation != policy.Generation {
		return Violation
	}
	return Conform
}

// step 执行一个已满足偏序前驱的事件。idx 是事件在输入切片中的下标，用于
// 已执行位集；非法迁移返回 ok=false，调用方不得把该交错计入违规证据。
func step(s *kernelState, idx int, ev Event) (*kernelState, string, Outcome, bool) {
	thread := int32(ev.Thread)
	n := cloneState(s)
	n.executed = s.executed | (uint64(1) << uint(idx))

	switch ev.Kind {
	case EnterRequest:
		n.active[thread] = append(n.active[thread], frame{token: ev.Token, kind: enterFrame})
	case Borrow:
		n.active[thread] = append(n.active[thread], frame{
			token:    ev.Token,
			kind:     borrowFrame,
			borrowID: ev.BorrowID,
			thread:   thread,
		})
	case Restore:
		stack := n.active[thread]
		if len(stack) == 0 {
			return nil, ReasonEmptyStack, 0, false
		}
		top := stack[len(stack)-1]
		if top.kind != borrowFrame {
			return nil, ReasonLIFO, 0, false
		}
		if ev.BorrowID >= 0 {
			if top.thread != thread {
				return nil, ReasonForeignBorrow, 0, false
			}
			if top.borrowID != ev.BorrowID {
				return nil, ReasonLIFO, 0, false
			}
		} else if top.borrowID >= 0 {
			return nil, ReasonLIFO, 0, false
		}
		n.active[thread] = stack[:len(stack)-1]
	case Spawn:
		stack := n.active[thread]
		token, ok := effectiveToken(stack)
		if !ok {
			return nil, ReasonEmptyStack, 0, false
		}
		n.slots[ev.Slot] = slotState{token: token, gen: ev.HandleGeneration, valid: true}
	case Suspend:
		stack := n.active[thread]
		if len(stack) == 0 {
			return nil, ReasonEmptyStack, 0, false
		}
		top := stack[len(stack)-1]
		n.active[thread] = stack[:len(stack)-1]
		n.suspended[thread] = append(n.suspended[thread], top)
	case ResumeHandle:
		key := handleKey{slot: ev.Slot, gen: ev.HandleGeneration}
		slot, ok := n.slots[ev.Slot]
		if !ok || !slot.valid || slot.gen != ev.HandleGeneration {
			return nil, ReasonStaleHandle, 0, false
		}
		if _, consumed := n.consumed[key]; consumed {
			return nil, ReasonConsumedHandle, 0, false
		}
		n.consumed[key] = struct{}{}
		n.active[thread] = append(n.active[thread], frame{token: slot.token, kind: snapshotFrame})
	case ProtectedRead:
		outcome := readOutcome(n.active[thread], ev.Policy)
		n.hash = stateHash(n)
		return n, "", outcome, true
	case Exit:
		if reason := exitCheck(n, thread); reason != "" {
			return nil, reason, 0, false
		}
	default:
		return nil, "unknown event kind", 0, false
	}
	n.hash = stateHash(n)
	return n, "", Conform, true
}

func exitCheck(s *kernelState, thread int32) string {
	for _, f := range s.active[thread] {
		if f.kind == borrowFrame {
			return ReasonBorrowsAtExit
		}
	}
	return ""
}

// stateHash 是状态的快速内部主键：直接对规范化字节做 FNV，避免每次迁移都
// 构造可读字符串。可读性指纹 fingerprint 仅在产出证书时按需计算。
func stateHash(s *kernelState) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], s.executed)
	h.Write(buf[:])

	threads := make([]int32, 0, len(s.active)+len(s.suspended))
	tSet := map[int32]struct{}{}
	for t := range s.active {
		tSet[t] = struct{}{}
	}
	for t := range s.suspended {
		tSet[t] = struct{}{}
	}
	for t := range tSet {
		threads = append(threads, t)
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i] < threads[j] })
	for _, t := range threads {
		binary.LittleEndian.PutUint32(buf[:4], uint32(t))
		h.Write(buf[:4])
		for _, f := range s.active[t] {
			hashFrame(h, f)
		}
		h.Write([]byte{0})
		for _, f := range s.suspended[t] {
			hashFrame(h, f)
		}
		h.Write([]byte{0})
	}

	slots := make([]int, 0, len(s.slots))
	for k := range s.slots {
		slots = append(slots, k)
	}
	sort.Ints(slots)
	for _, k := range slots {
		v := s.slots[k]
		binary.LittleEndian.PutUint32(buf[:4], uint32(k))
		h.Write(buf[:4])
		h.Write([]byte{byte(v.gen), byte(v.token.Generation)})
		h.Write([]byte(v.token.Tenant))
	}

	consumed := make([]handleKey, 0, len(s.consumed))
	for k := range s.consumed {
		consumed = append(consumed, k)
	}
	sort.Slice(consumed, func(i, j int) bool {
		if consumed[i].slot != consumed[j].slot {
			return consumed[i].slot < consumed[j].slot
		}
		return consumed[i].gen < consumed[j].gen
	})
	for _, k := range consumed {
		binary.LittleEndian.PutUint32(buf[:4], uint32(k.slot))
		h.Write(buf[:4])
	}
	return h.Sum64()
}

func hashFrame(h hash.Hash64, f frame) {
	var buf [16]byte
	binary.LittleEndian.PutUint32(buf[:4], uint32(f.kind))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(f.borrowID))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(f.thread))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(f.token.Generation))
	h.Write(buf[:])
	h.Write([]byte(f.token.Tenant))
}

func fingerprint(s *kernelState) string {
	var b strings.Builder
	b.WriteString("E=")
	b.WriteString(strconv.FormatUint(s.executed, 36))

	threads := make([]int32, 0, len(s.active)+len(s.suspended))
	tSet := map[int32]struct{}{}
	for t := range s.active {
		tSet[t] = struct{}{}
	}
	for t := range s.suspended {
		tSet[t] = struct{}{}
	}
	for t := range tSet {
		threads = append(threads, t)
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i] < threads[j] })
	for _, t := range threads {
		fmt.Fprintf(&b, "|T%d/A:", t)
		for _, f := range s.active[t] {
			b.WriteString(encodeFrame(f))
			b.WriteByte(';')
		}
		b.WriteString("/S:")
		for _, f := range s.suspended[t] {
			b.WriteString(encodeFrame(f))
			b.WriteByte(';')
		}
	}

	slots := make([]int, 0, len(s.slots))
	for k := range s.slots {
		slots = append(slots, k)
	}
	sort.Ints(slots)
	for _, k := range slots {
		v := s.slots[k]
		fmt.Fprintf(&b, "|H%d=%t:%d:%s#%d", k, v.valid, v.gen, v.token.Tenant, v.token.Generation)
	}

	consumed := make([]handleKey, 0, len(s.consumed))
	for k := range s.consumed {
		consumed = append(consumed, k)
	}
	sort.Slice(consumed, func(i, j int) bool {
		if consumed[i].slot != consumed[j].slot {
			return consumed[i].slot < consumed[j].slot
		}
		return consumed[i].gen < consumed[j].gen
	})
	b.WriteString("|C=")
	for _, k := range consumed {
		fmt.Fprintf(&b, "%d:%d;", k.slot, k.gen)
	}

	return b.String()
}

func encodeFrame(f frame) string {
	return fmt.Sprintf("%d(%s#%d,b%d,t%d)", f.kind, f.token.Tenant, f.token.Generation, f.borrowID, f.thread)
}

func modelFingerprint(in Input) string {
	h := fnv.New64a()
	for _, ev := range in.Events {
		fmt.Fprintf(h, "%d|%d|%d|%d|%s|%d|%d|%d|%d|%s|%d|%v\n",
			ev.ID, ev.Chain, ev.Thread, ev.Kind, ev.Token.Tenant, ev.Token.Generation,
			ev.Slot, ev.HandleGeneration, ev.BorrowID, ev.Policy.Tenant, ev.Policy.Generation, ev.After)
	}
	return strconv.FormatUint(h.Sum64(), 36)
}
