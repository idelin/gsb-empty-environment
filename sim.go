package twopc

import "fmt"

// Message 是节点间唯一的消息类型。
type Message struct {
	From, To string
	Type     string // prepare / vote / commit / abort / ack
	TxID     string
	Yes      bool // vote 专用
}

type event struct {
	at  int64
	seq int
	msg Message
}

// Sim 是单进程事件模拟器：通信（投递/延迟/丢弃）、故障（崩溃/重启）
// 与时间（虚拟时钟）全部在这里受控。崩溃节点的发送会被丢弃，
// 重启节点的内存状态仅从各自 WAL 重建。
type Sim struct {
	Now    int64
	Coords map[string]*Coordinator
	Parts  map[string]*Participant
	Trace  []string

	q       []event
	seq     int
	crashed map[string]bool
}

func NewSim() *Sim {
	return &Sim{
		Coords:  map[string]*Coordinator{},
		Parts:   map[string]*Participant{},
		crashed: map[string]bool{},
	}
}

func (s *Sim) AddCoordinator(c *Coordinator) { s.Coords[c.ID] = c }
func (s *Sim) AddParticipant(p *Participant) { s.Parts[p.ID] = p }

// Send 由节点回调：崩溃节点发不出消息（进程已死）。
func (s *Sim) Send(m Message) {
	if s.crashed[m.From] {
		s.tracef("t=%03d SEND-DROPPED  %-4s -> %-4s %s tx=%s (sender crashed)", s.Now, m.From, m.To, m.Type, m.TxID)
		return
	}
	s.enqueue(m)
}

// Inject 由测试/演示直接注入一条消息（如重复 commit、迟到 prepare）。
func (s *Sim) Inject(m Message) { s.enqueue(m) }

func (s *Sim) enqueue(m Message) {
	s.seq++
	s.q = append(s.q, event{at: s.Now + 1, seq: s.seq, msg: m})
}

// Step 投递最早的一条消息；返回 false 表示事件队列已空（系统静止）。
func (s *Sim) Step() bool {
	if len(s.q) == 0 {
		return false
	}
	best := 0
	for i := range s.q {
		if s.q[i].at < s.q[best].at || (s.q[i].at == s.q[best].at && s.q[i].seq < s.q[best].seq) {
			best = i
		}
	}
	e := s.q[best]
	s.q = append(s.q[:best], s.q[best+1:]...)
	s.Now = e.at
	m := e.msg
	if s.crashed[m.To] {
		s.tracef("t=%03d RECV-DROPPED  %-4s -> %-4s %s tx=%s (receiver crashed)", s.Now, m.From, m.To, m.Type, m.TxID)
		return true
	}
	yes := ""
	if m.Type == "vote" {
		yes = fmt.Sprintf(" yes=%v", m.Yes)
	}
	s.tracef("t=%03d %-4s -> %-4s %-8s tx=%s%s", s.Now, m.From, m.To, m.Type, m.TxID, yes)
	switch m.Type {
	case "prepare", "commit", "abort":
		p := s.Parts[m.To]
		switch m.Type {
		case "prepare":
			p.OnPrepare(m)
		case "commit":
			p.OnCommit(m)
		case "abort":
			p.OnAbort(m)
		}
	case "vote":
		s.Coords[m.To].OnVote(m)
	case "ack":
		s.Coords[m.To].OnAck(m)
	}
	return true
}

// RunUntilIdle 跑到系统静止（无待投递消息）。
func (s *Sim) RunUntilIdle() {
	for s.Step() {
	}
}

func (s *Sim) Pending() int { return len(s.q) }

// Crash 立即杀死节点：丢弃其全部易失状态（此后发送被丢弃、投递被丢弃）。
func (s *Sim) Crash(id string) {
	s.crashed[id] = true
	s.tracef("t=%03d *** CRASH %s ***", s.Now, id)
}

// Restart 重启节点：仅从 WAL 恢复状态；协调者还会按日志重放/补齐决策。
func (s *Sim) Restart(id string) {
	s.crashed[id] = false
	s.tracef("t=%03d *** RESTART %s (recover from WAL) ***", s.Now, id)
	if c, ok := s.Coords[id]; ok {
		c.Recover()
	}
	if p, ok := s.Parts[id]; ok {
		p.Recover()
	}
}

func (s *Sim) tracef(format string, args ...any) {
	s.Trace = append(s.Trace, fmt.Sprintf(format, args...))
}
