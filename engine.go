package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// WaitRef 返回某个等待事件在 Cancel.Ref 中应使用的引用文本。
// 取消事件通过等待事件的原始下标引用它，包内不会重排事件，因此下标稳定。
func WaitRef(waitIndex int) string {
	return strconv.Itoa(waitIndex)
}

// validate 校验候选事件的结构与字段约定。
func validate(events []Event) error {
	if len(events) == 0 {
		return ErrNoEvents
	}
	if len(events) > MaxCandidates {
		return fmt.Errorf("%w: got %d", ErrTooManyCandidates, len(events))
	}
	for i := range events {
		e := &events[i]
		if strings.TrimSpace(e.Actor) == "" {
			return fmt.Errorf("netsec: event %d has empty actor", i)
		}
		switch e.Kind {
		case KindWait:
			if len(e.Resources) == 0 {
				return fmt.Errorf("netsec: wait event %d must list at least one resource", i)
			}
			for _, r := range e.Resources {
				if strings.TrimSpace(r) == "" {
					return fmt.Errorf("netsec: wait event %d contains an empty resource", i)
				}
			}
		case KindProduce:
			if strings.TrimSpace(e.Produces) == "" {
				return fmt.Errorf("netsec: produce event %d missing Produces", i)
			}
		case KindCancel:
			target, err := strconv.Atoi(strings.TrimSpace(e.Ref))
			if err != nil || target < 0 || target >= len(events) {
				return fmt.Errorf("netsec: cancel event %d has invalid wait Ref %q", i, e.Ref)
			}
			if events[target].Kind != KindWait {
				return fmt.Errorf("netsec: cancel event %d points at non-wait event %d", i, target)
			}
			if events[target].Actor != e.Actor {
				return fmt.Errorf("netsec: cancel event %d targets wait %d of another actor", i, target)
			}
		case KindSend, KindReceive:
			if strings.TrimSpace(e.Ref) == "" {
				return fmt.Errorf("netsec: %s event %d missing message Ref", e.Kind, i)
			}
		default:
			return fmt.Errorf("netsec: event %d has unknown kind %d", i, e.Kind)
		}
	}
	return nil
}

// engine 持有一次性预处理好的 intern 表与事件投影，所有重放共用。
type engine struct {
	events []Event

	actors  []string       // 按首次出现顺序
	actorID map[string]int // 名称 -> actor 编号

	resName map[string]int // 资源名 -> 编号
	resID   []string       // 编号 -> 资源名
	msgName map[string]int // 消息名 -> 编号
	msgID   []string       // 编号 -> 消息名

	actorOf       []int   // 每个事件的 actor 编号
	waitResources [][]int // 等待事件的候选资源编号
	producedRes   []int   // produce/receive 事件产出的资源编号，-1 表示无
	msgRef        []int   // send/receive 事件的消息编号，-1 表示无
	cancelTarget  []int   // cancel 事件指向的等待下标，-1 表示无

	resProducers map[int][]int // 资源编号 -> 可生产它的事件下标
	msgSender    map[int]int   // 消息编号 -> 发送该消息的事件下标，-1/缺失表示从未发送
}

func newEngine(events []Event) *engine {
	en := &engine{
		events:        events,
		actorID:       map[string]int{},
		resName:       map[string]int{},
		msgName:       map[string]int{},
		actorOf:       make([]int, len(events)),
		waitResources: make([][]int, len(events)),
		producedRes:   make([]int, len(events)),
		msgRef:        make([]int, len(events)),
		cancelTarget:  make([]int, len(events)),
	}
	for i := range en.producedRes {
		en.producedRes[i] = -1
		en.msgRef[i] = -1
		en.cancelTarget[i] = -1
	}
	internRes := func(key string) int {
		if id, ok := en.resName[key]; ok {
			return id
		}
		id := len(en.resID)
		en.resName[key] = id
		en.resID = append(en.resID, key)
		return id
	}
	internMsg := func(key string) int {
		if id, ok := en.msgName[key]; ok {
			return id
		}
		id := len(en.msgID)
		en.msgName[key] = id
		en.msgID = append(en.msgID, key)
		return id
	}
	for i, e := range events {
		aid, ok := en.actorID[e.Actor]
		if !ok {
			aid = len(en.actors)
			en.actorID[e.Actor] = aid
			en.actors = append(en.actors, e.Actor)
		}
		en.actorOf[i] = aid

		switch e.Kind {
		case KindWait:
			ids := make([]int, 0, len(e.Resources))
			for _, r := range e.Resources {
				ids = append(ids, internRes(r))
			}
			en.waitResources[i] = ids
		case KindProduce:
			en.producedRes[i] = internRes(e.Produces)
		case KindReceive:
			if e.Produces != "" {
				en.producedRes[i] = internRes(e.Produces)
			}
			en.msgRef[i] = internMsg(e.Ref)
		case KindSend:
			en.msgRef[i] = internMsg(e.Ref)
		case KindCancel:
			target, _ := strconv.Atoi(e.Ref)
			en.cancelTarget[i] = target
		}
	}
	en.resProducers = map[int][]int{}
	en.msgSender = map[int]int{}
	for i, e := range events {
		switch e.Kind {
		case KindProduce, KindReceive:
			if en.producedRes[i] >= 0 {
				en.resProducers[en.producedRes[i]] = append(en.resProducers[en.producedRes[i]], i)
			}
		case KindSend:
			en.msgSender[en.msgRef[i]] = i
		}
	}
	return en
}

// state 是单次重放的可变运行态。
type state struct {
	blocked      []bool // 每个 actor 是否卡在未解除的等待上
	pending      []int  // 每个 actor 当前挂起的事件下标，-1 表示无
	resAvailable []bool
	msgAvailable []bool
}

func (en *engine) freshState() *state {
	return &state{
		blocked:      make([]bool, len(en.actors)),
		pending:      make([]int, len(en.actors)),
		resAvailable: make([]bool, len(en.resName)),
		msgAvailable: make([]bool, len(en.msgName)),
	}
}

func (en *engine) waitSatisfied(st *state, waitIndex int) (int, bool) {
	for _, rid := range en.waitResources[waitIndex] {
		if st.resAvailable[rid] {
			return rid, true
		}
	}
	return -1, false
}

// releaseWith 标记资源 rid 可用，并解除所有可被它择一满足的等待，返回被解除的等待下标。
func (en *engine) releaseWith(st *state, rid int) []int {
	st.resAvailable[rid] = true
	released := make([]int, 0)
	for aid := range en.actors {
		if !st.blocked[aid] {
			continue
		}
		w := st.pending[aid]
		if w < 0 || en.events[w].Kind != KindWait {
			continue
		}
		for _, cand := range en.waitResources[w] {
			if cand == rid {
				st.blocked[aid] = false
				st.pending[aid] = -1
				released = append(released, w)
				break
			}
		}
	}
	return released
}

// replay 对 mask 选中的事件子集做一次具体重放。
// 事件严格按原始下标顺序处理；被跳过的事件不产生任何副作用，也不会被补造。
func (en *engine) replay(mask uint32, recordSteps bool) ReplayReport {
	st := en.freshState()
	report := ReplayReport{}
	var steps []Step
	if recordSteps {
		steps = make([]Step, 0, bitsOn(mask))
		report.Included = make([]int, 0, bitsOn(mask))
	}

	appendStep := func(s Step) {
		if recordSteps {
			steps = append(steps, s)
		}
	}

	for i := range en.events {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		e := &en.events[i]
		aid := en.actorOf[i]
		if recordSteps {
			report.Included = append(report.Included, i)
		}

		if st.blocked[aid] && e.Kind != KindCancel {
			report.Skipped = append(report.Skipped, i)
			appendStep(Step{
				Index:   i,
				Kind:    e.Kind,
				Actor:   e.Actor,
				Applied: false,
				Outcome: fmt.Sprintf("skipped: actor %q blocked on event #%d", e.Actor, st.pending[aid]),
			})
			continue
		}

		var outcome string
		switch e.Kind {
		case KindWait:
			if rid, ok := en.waitSatisfied(st, i); ok {
				outcome = fmt.Sprintf("wait satisfied immediately by available resource %q", en.resID[rid])
			} else {
				st.blocked[aid] = true
				st.pending[aid] = i
				outcome = fmt.Sprintf("blocked: waiting for any of %v", e.Resources)
			}
			appendStep(Step{Index: i, Kind: e.Kind, Actor: e.Actor, Applied: true, Outcome: outcome})

		case KindProduce:
			rid := en.producedRes[i]
			released := en.releaseWith(st, rid)
			outcome = fmt.Sprintf("resource %q produced", e.Produces)
			appendStep(Step{Index: i, Kind: e.Kind, Actor: e.Actor, Applied: true, Outcome: outcome})
			for _, w := range released {
				appendStep(Step{
					Index:   w,
					Kind:    KindWait,
					Actor:   en.events[w].Actor,
					Applied: true,
					Outcome: fmt.Sprintf("wait #%d released by resource %q from event #%d", w, e.Produces, i),
				})
			}

		case KindSend:
			mid := en.msgRef[i]
			st.msgAvailable[mid] = true
			outcome = fmt.Sprintf("message %q sent and now available", e.Ref)
			appendStep(Step{Index: i, Kind: e.Kind, Actor: e.Actor, Applied: true, Outcome: outcome})

		case KindReceive:
			mid := en.msgRef[i]
			if !st.msgAvailable[mid] {
				st.blocked[aid] = true
				st.pending[aid] = i
				appendStep(Step{
					Index:   i,
					Kind:    e.Kind,
					Actor:   e.Actor,
					Applied: true,
					Outcome: fmt.Sprintf("blocked: message %q was never sent within the subset", e.Ref),
				})
				break
			}
			outcome = fmt.Sprintf("message %q received from a real send", e.Ref)
			if rid := en.producedRes[i]; rid >= 0 {
				released := en.releaseWith(st, rid)
				appendStep(Step{Index: i, Kind: e.Kind, Actor: e.Actor, Applied: true, Outcome: outcome + fmt.Sprintf("; resource %q produced", e.Produces)})
				for _, w := range released {
					appendStep(Step{
						Index:   w,
						Kind:    KindWait,
						Actor:   en.events[w].Actor,
						Applied: true,
						Outcome: fmt.Sprintf("wait #%d released by resource %q from receive #%d", w, e.Produces, i),
					})
				}
				break
			}
			appendStep(Step{Index: i, Kind: e.Kind, Actor: e.Actor, Applied: true, Outcome: outcome})

		case KindCancel:
			target := en.cancelTarget[i]
			if st.blocked[aid] && st.pending[aid] == target {
				st.blocked[aid] = false
				st.pending[aid] = -1
				outcome = fmt.Sprintf("wait #%d cancelled", target)
			} else {
				outcome = fmt.Sprintf("cancel of wait #%d had no pending target in this subset", target)
			}
			appendStep(Step{Index: i, Kind: e.Kind, Actor: e.Actor, Applied: true, Outcome: outcome})
		}
	}

	for aid := range en.actors {
		if st.blocked[aid] {
			// pending 记录该方真正停住的卡点事件：未解除的等待，或等不到消息的接收。
			report.Pending = append(report.Pending, st.pending[aid])
			report.BlockedActors = append(report.BlockedActors, en.actors[aid])
		}
	}
	sort.Ints(report.Pending)
	sort.Ints(report.Skipped)
	sort.Strings(report.BlockedActors)

	if recordSteps {
		report.Steps = steps
		report.AvailableResources = en.availableResourceNames(st)
		report.AvailableMessages = en.availableMessageNames(st)
	}
	return report
}

func (en *engine) availableResourceNames(st *state) []string {
	names := make([]string, 0)
	for name, id := range en.resName {
		if st.resAvailable[id] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (en *engine) availableMessageNames(st *state) []string {
	names := make([]string, 0)
	for name, id := range en.msgName {
		if st.msgAvailable[id] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func bitsOn(mask uint32) int {
	n := 0
	for mask != 0 {
		mask &= mask - 1
		n++
	}
	return n
}

// ReplaySubset 对宿主指定的事件下标子集做一次可核对的具体重放。
// 下标按原始真实顺序处理；未选中的事件不参与，也不会被任何合成事件替代。
func ReplaySubset(events []Event, indices []int) (ReplayReport, error) {
	if err := validate(events); err != nil {
		return ReplayReport{}, err
	}
	var mask uint32
	seen := map[int]bool{}
	for _, idx := range indices {
		if idx < 0 || idx >= len(events) {
			return ReplayReport{}, fmt.Errorf("netsec: subset index %d out of range", idx)
		}
		if seen[idx] {
			return ReplayReport{}, fmt.Errorf("netsec: duplicate subset index %d", idx)
		}
		seen[idx] = true
		mask |= 1 << uint(idx)
	}
	en := newEngine(events)
	return en.replay(mask, true), nil
}
