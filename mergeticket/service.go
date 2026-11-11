package mergeticket

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// 票据生命周期状态。
const (
	StateOpen      = "open"
	StateMerged    = "merged"
	StateCancelled = "cancelled"
	StateInvalid   = "invalid" // 三重绑定与当前目标状态不再匹配
)

// 事件类型。所有状态迁移与队列重排都只通过事件日志对外可观察。
const (
	EventApproved  = "approved"
	EventReordered = "reordered"
	EventMerged    = "merged"
	EventInvalid   = "invalid"
	EventCancelled = "cancelled"
)

// 哨兵错误：调用方可用 errors.Is 精确区分终态与绑定失效。
var (
	ErrUnknownTicket    = errors.New("mergeticket: unknown ticket")
	ErrTerminal         = errors.New("mergeticket: ticket is in a terminal state")
	ErrCancelled        = fmt.Errorf("%w: cancelled", ErrTerminal)
	ErrAlreadyMerged    = fmt.Errorf("%w: already merged", ErrTerminal)
	ErrStaleCandidate   = errors.New("mergeticket: candidate commit does not match the approval")
	ErrBaselineAdvanced = errors.New("mergeticket: target baseline advanced since approval")
	ErrPolicyChanged    = errors.New("mergeticket: policy version changed since approval")
	ErrInvalidPosition  = errors.New("mergeticket: invalid queue position")
	ErrUnknownRef       = errors.New("mergeticket: target ref does not exist")
)

// Policy 是合并策略的版本化快照。版本号必须单调递增；策略一旦升级，
// 在旧版本下批准的票据即失去合入资格。
type Policy struct {
	Version   int
	Name      string
	RequireCI bool
}

// Ticket 是一张把"批准"冻结到具体三元组上的合并票据。
// 批准绑定的不是拉取请求编号，而是：
//   - CandidateCommit / CandidateTree：被审过的候选提交及其树；
//   - Baseline：批准时目标分支所指向的基线提交；
//   - PolicyVersion：批准时生效的策略版本。
//
// 合入时三元组必须与现场逐一核对，任何一项漂移都会使票据失效。
type Ticket struct {
	ID              string
	Target          string
	CandidateCommit Hash
	CandidateTree   Hash
	Baseline        Hash
	PolicyVersion   int
	State           string
}

// Event 是事件日志中的一条记录。Seq 在整个 Service 内单调递增，
// 是并发消费与撤回的唯一可观察总顺序。
type Event struct {
	Seq          int64
	Type         string
	TicketID     string
	Target       string
	FromPosition int  // Reordered 事件：重排前位置；-1 表示不适用
	ToPosition   int  // Reordered 事件：重排后位置；-1 表示不适用
	OldBaseline  Hash // 基线推进相关事件的旧基线
	NewBaseline  Hash
	Err          string // Invalid 事件：失效原因
	At           time.Time
}

// Service 在内存提交图之上管理每个目标分支的合并票据队列。
// 所有可变状态都由单一互斥锁保护，状态迁移与事件追加在同一临界区
// 原子完成，因此事件日志的 Seq 顺序与真实发生顺序严格一致。
type Service struct {
	graph *Graph

	mu      sync.Mutex
	cond    sync.Cond
	seq     int64
	journal []Event

	tickets map[string]*Ticket
	queues  map[string][]string // 目标分支 -> 票据 ID 队列（仅含 open 票据）
	policy  map[string]Policy
}

// NewService 创建票据服务。
func NewService(graph *Graph) *Service {
	s := &Service{
		graph:   graph,
		tickets: make(map[string]*Ticket),
		queues:  make(map[string][]string),
		policy:  make(map[string]Policy),
	}
	s.cond.L = &s.mu
	return s
}

// SetPolicy 为目标分支登记策略。新版本号必须大于当前版本。
func (s *Service) SetPolicy(target string, p Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.policy[target]; ok && p.Version <= cur.Version {
		return fmt.Errorf("mergeticket: policy version must increase (have %d, got %d)", cur.Version, p.Version)
	}
	s.policy[target] = p
	return nil
}

// PolicyVersion 返回目标分支当前策略版本；未登记时为 0。
func (s *Service) PolicyVersion(target string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy[target].Version
}

// Approve 把一次代码审查批准冻结为合并票据。
// 批准时刻会快照候选提交、候选树、目标基线与策略版本，之后任何漂移
// 都无法通过 Consume 的核对。
func (s *Service) Approve(id, target string, candidate Hash) (*Ticket, error) {
	commit, err := s.graph.Lookup(candidate)
	if err != nil {
		return nil, err
	}
	baseline := s.graph.Ref(target)
	if baseline == "" {
		return nil, ErrUnknownRef
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tickets[id]; exists {
		return nil, fmt.Errorf("mergeticket: duplicate ticket id %q", id)
	}
	policy, ok := s.policy[target]
	if !ok {
		return nil, fmt.Errorf("mergeticket: no policy configured for target %q", target)
	}

	t := &Ticket{
		ID:              id,
		Target:          target,
		CandidateCommit: candidate,
		CandidateTree:   commit.Tree,
		Baseline:        baseline,
		PolicyVersion:   policy.Version,
		State:           StateOpen,
	}
	s.tickets[id] = t
	s.queues[target] = append(s.queues[target], id)
	s.appendLocked(Event{
		Type:        EventApproved,
		TicketID:    id,
		Target:      target,
		NewBaseline: baseline,
	})
	return cloneTicket(t), nil
}

// Consume 尝试合入一张票据。它在同一临界区内完成"现场核对 -> 快进
// 目标分支 -> 状态迁移 -> 事件追加"，因此并发重试至多一个胜出，且
// 胜出者合入的一定是批准时冻结的那个候选提交。
func (s *Service) Consume(id string) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.requireOpen(id)
	if err != nil {
		return nil, err
	}

	// 现场核对 1：候选提交必须仍是批准时冻结的那个提交。
	// 只比树不够：相同树挂在不同父提交上是另一个提交，携带的是
	// 没人审过的历史。
	commit, err := s.graph.Lookup(t.CandidateCommit)
	if err != nil {
		return nil, s.rejectLocked(t, ErrStaleCandidate)
	}
	if commit.Tree != t.CandidateTree {
		return nil, s.rejectLocked(t, ErrStaleCandidate)
	}

	// 现场核对 2：目标基线必须仍是批准时快照的基线。
	// 队列重排后别人先合入、基线推进，旧票据即刻失效。
	currentBaseline := s.graph.Ref(t.Target)
	if currentBaseline != t.Baseline {
		return nil, s.rejectLocked(t, fmt.Errorf("%w: approved %s, now %s", ErrBaselineAdvanced, t.Baseline, currentBaseline))
	}

	// 现场核对 3：策略版本必须仍是批准时的版本。
	if s.policy[t.Target].Version != t.PolicyVersion {
		return nil, s.rejectLocked(t, fmt.Errorf("%w: approved v%d, now v%d", ErrPolicyChanged, t.PolicyVersion, s.policy[t.Target].Version))
	}

	// 核对通过：快进目标分支到被批准的候选提交。
	if err := s.graph.SetRef(t.Target, t.CandidateCommit); err != nil {
		return nil, s.rejectLocked(t, err)
	}

	t.State = StateMerged
	s.removeFromQueueLocked(t.Target, id)
	s.appendLocked(Event{
		Type:        EventMerged,
		TicketID:    id,
		Target:      t.Target,
		OldBaseline: t.Baseline,
		NewBaseline: t.CandidateCommit,
	})
	return cloneTicket(t), nil
}

// Cancel 撤回一张 open 票据。与 Consume 竞争时二者在事件日志中
// 只有一个生效，顺序由 Seq 决定。
func (s *Service) Cancel(id string) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.requireOpen(id)
	if err != nil {
		return nil, err
	}
	t.State = StateCancelled
	s.removeFromQueueLocked(t.Target, id)
	s.appendLocked(Event{Type: EventCancelled, TicketID: id, Target: t.Target})
	return cloneTicket(t), nil
}

// Reorder 重排目标分支的合并队列：把票据 from 移动到 to。
// 重排本身不改变任何票据的绑定，只产生一条 Reordered 事件；真正的
// 安全闸口仍在 Consume 的三重核对里。
func (s *Service) Reorder(target string, from, to int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := s.queues[target]
	if from < 0 || from >= len(q) || to < 0 || to >= len(q) {
		return ErrInvalidPosition
	}
	if from == to {
		return nil
	}
	id := q[from]
	q = append(q[:from], q[from+1:]...)
	q = append(q[:to], append([]string{id}, q[to:]...)...)
	s.queues[target] = q
	s.appendLocked(Event{
		Type:         EventReordered,
		TicketID:     id,
		Target:       target,
		FromPosition: from,
		ToPosition:   to,
	})
	return nil
}

// AdvanceBaseline 模拟目标分支被外部（如其他队列先合入）快进到 newBaseline。
// 之后所有仍引用旧基线的 open 票据都会在 Consume 时被拒。
func (s *Service) AdvanceBaseline(target string, newBaseline Hash) error {
	if err := s.graph.SetRef(target, newBaseline); err != nil {
		return err
	}
	s.mu.Lock()
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

// Queue 返回目标分支当前 open 票据 ID 的快照（队首在下标 0）。
func (s *Service) Queue(target string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queues[target]...)
}

// Head 返回队首票据 ID；队列为空时返回空串。
func (s *Service) Head(target string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queues[target]) == 0 {
		return ""
	}
	return s.queues[target][0]
}

// Ticket 返回票据快照。
func (s *Service) Ticket(id string) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok {
		return nil, ErrUnknownTicket
	}
	return cloneTicket(t), nil
}

// Journal 返回事件日志的完整快照。
func (s *Service) Journal() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.journal...)
}

// WaitForSequence 阻塞直到事件日志出现 afterSeq 之后的事件并返回它们。
// 所有观察者通过同一个条件变量看到同一条 Seq 总顺序。
// timeout <= 0 时无限等待。
func (s *Service) WaitForSequence(afterSeq int64, timeout time.Duration) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for int64(len(s.journal)) <= afterSeq {
		if timeout <= 0 {
			s.cond.Wait()
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, errors.New("mergeticket: wait timed out")
		}
		timer := time.AfterFunc(remaining, s.cond.Broadcast)
		s.cond.Wait()
		timer.Stop()
		if time.Now().After(deadline) && int64(len(s.journal)) <= afterSeq {
			return nil, errors.New("mergeticket: wait timed out")
		}
	}
	return append([]Event(nil), s.journal[afterSeq:]...), nil
}

// requireOpen 返回 open 票据；终态票据返回包装了 ErrTerminal 的错误，
// 保证重试不会对已合入/已撤回/已失效的票据产生第二次状态迁移。
func (s *Service) requireOpen(id string) (*Ticket, error) {
	t, ok := s.tickets[id]
	if !ok {
		return nil, ErrUnknownTicket
	}
	switch t.State {
	case StateMerged:
		return nil, fmt.Errorf("%w (%s)", ErrAlreadyMerged, id)
	case StateCancelled:
		return nil, fmt.Errorf("%w (%s)", ErrCancelled, id)
	case StateInvalid:
		return nil, fmt.Errorf("%w (%s): invalid", ErrTerminal, id)
	default:
		return t, nil
	}
}

// rejectLocked 将票据永久标记为 invalid 并记录失效事件。
func (s *Service) rejectLocked(t *Ticket, cause error) error {
	t.State = StateInvalid
	s.removeFromQueueLocked(t.Target, t.ID)
	s.appendLocked(Event{
		Type:     EventInvalid,
		TicketID: t.ID,
		Target:   t.Target,
		Err:      cause.Error(),
	})
	return cause
}

func (s *Service) removeFromQueueLocked(target, id string) {
	q := s.queues[target]
	for i, queued := range q {
		if queued == id {
			s.queues[target] = append(q[:i], q[i+1:]...)
			return
		}
	}
}

// appendLocked 在临界区内追加事件并广播，Seq 即全局总顺序。
func (s *Service) appendLocked(e Event) {
	if e.Type != EventReordered {
		e.FromPosition, e.ToPosition = -1, -1
	}
	s.seq++
	e.Seq = s.seq
	e.At = time.Now()
	s.journal = append(s.journal, e)
	s.cond.Broadcast()
}

func cloneTicket(t *Ticket) *Ticket {
	copy := *t
	return &copy
}
