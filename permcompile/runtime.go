package permcompile

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// nodeState 是单个节点在某个可能世界中的状态。
type nodeState int

const (
	stPending   nodeState = iota // 尚未到达（顺序后继、未选中分支等）
	stActive                     // 可参与消费或仍在等待
	stCompleted                  // 子树合法完成
	stCanceled                   // 被取消，永不复活
)

// leafState 是某个世界中一片叶子的剩余配额。
type leafState struct {
	bytes IntervalSet // 尚未消费的字节区间
	reads IntervalSet // 尚未消费的读取次数区间
}

// world 是一种尚可成立的执行解释。运行时始终保留全部可能世界，
// 只有已认证事件或实际提交能淘汰不再相容的世界。
type world struct {
	state     []nodeState
	leaves    []leafState
	decide    map[int]string // 已认证决定的二选一门 -> 选项标识
	tentative map[int]string // 提交时暂定的门 -> 选项标识（尚未认证）
	visited   map[int]bool   // 回退主分支是否发生过消费
}

// Runtime 是一份计划在单个会话上的执行器，可嵌入导入宿主。
type Runtime struct {
	cp      *CompiledPlan
	session SessionID
	auth    Authenticator
	now     func() time.Time
	expiry  time.Time

	mu      sync.Mutex
	worlds  []*world
	nonces  map[string]bool
	expired bool
	trace   []TraceEntry
}

// Option 配置运行时。
type Option func(*Runtime)

// WithClock 注入测试时钟。
func WithClock(now func() time.Time) Option {
	return func(r *Runtime) { r.now = now }
}

// WithExpiry 设定会话过期时刻；零值表示不过期。
func WithExpiry(t time.Time) Option {
	return func(r *Runtime) { r.expiry = t }
}

// NewRuntime 在内存中构造执行器。
func NewRuntime(cp *CompiledPlan, session SessionID, auth Authenticator, opts ...Option) *Runtime {
	r := &Runtime{
		cp:      cp,
		session: session,
		auth:    auth,
		now:     time.Now,
		nonces:  map[string]bool{},
	}
	for _, o := range opts {
		o(r)
	}
	w := &world{
		state:     make([]nodeState, len(cp.nodes)),
		leaves:    make([]leafState, len(cp.leaves)),
		decide:    map[int]string{},
		tentative: map[int]string{},
		visited:   map[int]bool{},
	}
	for i, g := range cp.leaves {
		w.leaves[i] = leafState{
			bytes: newIntervalSet(g.scope.Lo, g.scope.Hi),
			reads: newIntervalSet(0, g.maxReads),
		}
	}
	w.state[0] = stActive
	r.worlds = []*world{w}
	r.refresh(w)
	return r
}

func (r *Runtime) checkExpiryLocked() bool {
	if r.expired {
		return true
	}
	if !r.expiry.IsZero() && !r.now().Before(r.expiry) {
		r.expired = true
	}
	return r.expired
}

func (w *world) key() string {
	var b strings.Builder
	for i, s := range w.state {
		fmt.Fprintf(&b, "%d:%d;", i, s)
	}
	b.WriteString("L{")
	for i, l := range w.leaves {
		fmt.Fprintf(&b, "%d:b=%s,r=%s;", i, l.bytes.key(), l.reads.key())
	}
	b.WriteString("}D{")
	gates := make([]int, 0, len(w.decide))
	for g := range w.decide {
		gates = append(gates, g)
	}
	sort.Ints(gates)
	for _, g := range gates {
		fmt.Fprintf(&b, "%d=%s;", g, w.decide[g])
	}
	b.WriteString("}T{")
	tent := make([]int, 0, len(w.tentative))
	for g := range w.tentative {
		tent = append(tent, g)
	}
	sort.Ints(tent)
	for _, g := range tent {
		fmt.Fprintf(&b, "%d=%s;", g, w.tentative[g])
	}
	b.WriteString("}V{")
	visited := make([]int, 0, len(w.visited))
	for g := range w.visited {
		visited = append(visited, g)
	}
	sort.Ints(visited)
	for _, g := range visited {
		fmt.Fprintf(&b, "%d;", g)
	}
	b.WriteByte('}')
	return b.String()
}

func (w *world) clone() *world {
	nw := &world{
		state:     append([]nodeState(nil), w.state...),
		leaves:    make([]leafState, len(w.leaves)),
		decide:    make(map[int]string, len(w.decide)),
		tentative: make(map[int]string, len(w.tentative)),
		visited:   make(map[int]bool, len(w.visited)),
	}
	for i, l := range w.leaves {
		bytesCopy := make(IntervalSet, len(l.bytes))
		copy(bytesCopy, l.bytes)
		readsCopy := make(IntervalSet, len(l.reads))
		copy(readsCopy, l.reads)
		nw.leaves[i] = leafState{
			bytes: bytesCopy,
			reads: readsCopy,
		}
	}
	for k, v := range w.decide {
		nw.decide[k] = v
	}
	for k, v := range w.tentative {
		nw.tentative[k] = v
	}
	for k, v := range w.visited {
		nw.visited[k] = v
	}
	return nw
}

func (r *Runtime) refresh(w *world) { r.eval(w, 0) }

// eval 自上而下重算可达激活状态并自下而上闭合复合节点。
func (r *Runtime) eval(w *world, n int) nodeState {
	cp := r.cp
	fn := cp.nodes[n]
	switch w.state[n] {
	case stCanceled, stCompleted:
		return w.state[n]
	case stPending:
		return stPending
	}

	switch fn.kind {
	case KindLeaf:
		leaf := cp.leafOf[n]
		// 读取次数是唯一的耗尽维度；字节区间在完整读取后仍可凭剩余次数重读，
		// 只有部分读取会切分仍可读区间。
		if w.leaves[leaf].reads.empty() {
			w.state[n] = stCompleted
		}
		return w.state[n]

	case KindParallel:
		allDone := true
		for _, k := range fn.kids {
			if w.state[k] == stPending {
				w.state[k] = stActive
			}
			if r.eval(w, k) != stCompleted {
				allDone = false
			}
		}
		if allDone {
			w.state[n] = stCompleted
		}

	case KindSequence:
		for _, k := range fn.kids {
			if w.state[k] == stCompleted {
				continue
			}
			if w.state[k] == stPending {
				w.state[k] = stActive
			}
			r.eval(w, k)
			if w.state[k] != stCompleted {
				break // 后续节点保持 pending，满足部分先后约束
			}
		}
		if allOf(w, fn.kids, stCompleted) {
			w.state[n] = stCompleted
		}

	case KindChoice:
		opt, settled := w.decide[n]
		if !settled {
			opt, settled = w.tentative[n]
		}
		if !settled {
			return stActive // 等待绑定门的已认证选择事件
		}
		chosen := cp.choice[n].options[opt]
		for _, k := range fn.kids {
			if k == chosen {
				if w.state[k] == stPending {
					w.state[k] = stActive
				}
				r.eval(w, k)
			} else {
				r.setCanceled(w, k)
			}
		}
		if w.state[chosen] == stCompleted {
			w.state[n] = stCompleted
		}

	case KindFallback:
		primary, fallback := fn.kids[0], fn.kids[1]
		if w.state[fallback] == stActive || w.state[fallback] == stCompleted {
			r.eval(w, fallback)
			if w.state[fallback] == stCompleted {
				w.state[n] = stCompleted
			}
			break
		}
		if w.state[primary] == stPending {
			w.state[primary] = stActive
		}
		r.eval(w, primary)
		switch {
		case w.state[primary] == stCompleted:
			r.setCanceled(w, fallback)
			w.state[n] = stCompleted
		case w.visited[n]:
			// 回退事件已触发：切换到回退分支；主分支若有部分消费，其已扣配额保留。
			r.setCanceled(w, primary)
			w.state[fallback] = stActive
			r.eval(w, fallback)
			if w.state[fallback] == stCompleted {
				w.state[n] = stCompleted
			}
		}
	}
	return w.state[n]
}

// setCanceled 标记子树取消并清空其叶子配额；取消分支永不复活。
func (r *Runtime) setCanceled(w *world, n int) {
	if w.state[n] == stCanceled {
		return
	}
	w.state[n] = stCanceled
	fn := r.cp.nodes[n]
	if fn.kind == KindLeaf {
		leaf := r.cp.leafOf[n]
		w.leaves[leaf] = leafState{bytes: IntervalSet{}, reads: IntervalSet{}}
		return
	}
	for _, k := range fn.kids {
		r.setCanceled(w, k)
	}
}

func allOf(w *world, nodes []int, want nodeState) bool {
	for _, n := range nodes {
		if w.state[n] != want {
			return false
		}
	}
	return true
}

// applyDryRun 在不修改世界的前提下校验事件是否合法。
func (r *Runtime) applyDryRun(w *world, ev Event) error {
	cp := r.cp
	switch ev.Kind {
	case EventDecision:
		n, ok := cp.gateOf[ev.Gate]
		if !ok || cp.nodes[n].kind != KindChoice {
			return errUnknownGate
		}
		if _, ok := cp.choice[n].options[ev.Option]; !ok {
			return errUnknownOption
		}
		if cur, ok := w.decide[n]; ok && cur != ev.Option {
			return errGateSettled
		}
	case EventFallback:
		n, ok := cp.gateOf[ev.Gate]
		if !ok || cp.nodes[n].kind != KindFallback {
			return errBadFallback
		}
		primary := cp.nodes[n].kids[0]
		if w.state[primary] == stCompleted || w.state[primary] == stCanceled {
			return errGateSettled
		}
	case EventCancel:
		n, ok := cp.gateOf[ev.Gate]
		if !ok {
			return errUnknownGate
		}
		if w.state[n] == stCompleted || w.state[n] == stCanceled {
			return errCannotCancel
		}
	}
	return nil
}

func (r *Runtime) applyEvent(w *world, ev Event) {
	cp := r.cp
	switch ev.Kind {
	case EventDecision:
		n := cp.gateOf[ev.Gate]
		w.decide[n] = ev.Option
		delete(w.tentative, n)
	case EventFallback:
		n := cp.gateOf[ev.Gate]
		w.visited[n] = true
	case EventCancel:
		n := cp.gateOf[ev.Gate]
		r.setCanceled(w, n)
	}
	r.refresh(w)
}

// pruneAfterEvent 丢弃与已认证选择不再相容的暂定世界。
func (r *Runtime) pruneAfterEvent(ev Event) {
	if ev.Kind != EventDecision {
		return
	}
	gate := r.cp.gateOf[ev.Gate]
	kept := make([]*world, 0, len(r.worlds))
	for _, w := range r.worlds {
		cur, ok := w.decide[gate]
		tent, hasTent := w.tentative[gate]
		if (ok && cur != ev.Option) || (hasTent && tent != ev.Option) {
			continue
		}
		kept = append(kept, w)
	}
	r.worlds = kept
}

func (r *Runtime) checkEventBinding(ev Event) error {
	if ev.Session != r.session {
		return errBindSession
	}
	if ev.Digest != r.cp.digest {
		return errBindDigest
	}
	if r.nonces[ev.Nonce] {
		return errReplay
	}
	if r.auth == nil || !r.auth(ev) {
		return errUnauthenticated
	}
	return nil
}

// Ingest 接收一个已认证事件。绑定、签名、门状态在任何状态变更前校验。
func (r *Runtime) Ingest(ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.checkExpiryLocked() {
		return errExpired
	}
	if err := r.checkEventBinding(ev); err != nil {
		return err
	}
	for _, w := range r.worlds {
		if err := r.applyDryRun(w, ev); err != nil {
			return err
		}
	}
	r.nonces[ev.Nonce] = true
	r.pruneAfterEvent(ev)
	for _, w := range r.worlds {
		r.applyEvent(w, ev)
	}
	r.recordEvent(ev, "")
	return nil
}

// IngestBatch 原子接收同时到达的一批事件（如同时取消）。
// 任一事件不合法则整批拒绝，运行时保持不变。
func (r *Runtime) IngestBatch(evs []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.checkExpiryLocked() {
		return errExpired
	}
	for _, ev := range evs {
		if err := r.checkEventBinding(ev); err != nil {
			return err
		}
	}
	for _, w := range r.worlds {
		sim := w.clone()
		for _, ev := range evs {
			if err := r.applyDryRun(sim, ev); err != nil {
				return err
			}
			r.applyEvent(sim, ev)
		}
	}
	for _, ev := range evs {
		r.nonces[ev.Nonce] = true
	}
	for _, ev := range evs {
		r.pruneAfterEvent(ev)
		for _, w := range r.worlds {
			r.applyEvent(w, ev)
		}
	}
	for _, ev := range evs {
		r.recordEvent(ev, "batch")
	}
	return nil
}
