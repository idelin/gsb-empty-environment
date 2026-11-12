package netsec

import (
	"errors"
	"testing"
)

// kinds 从事件日志中提取类型序列，便于断言派发顺序。
func kinds(log []Event) []Kind {
	out := make([]Kind, len(log))
	for i, ev := range log {
		out[i] = ev.Kind
	}
	return out
}

func names(log []Event) []string {
	out := make([]string, len(log))
	for i, ev := range log {
		out[i] = ev.Name
	}
	return out
}

// TestTimeoutPriorityAtSameInstant 验证截止点一到，TimeoutEvent 必须
// 优先于同一时刻的普通零耗时事件派发。
func TestTimeoutPriorityAtSameInstant(t *testing.T) {
	var order []Kind
	s := NewScheduler(HandlerFunc(func(ev Event) {
		order = append(order, ev.Kind)
	}), 16)

	s.After("deadline", 5)
	if err := s.Advance(5); err != nil {
		t.Fatalf("advance to deadline: %v", err)
	}
	// 在截止时刻再排一条普通事件，并直接推进到同一时刻（delta=0，时钟不回退）。
	s.Post("late-message")
	if err := s.Advance(0); err != nil {
		t.Fatalf("drain at deadline: %v", err)
	}

	log := s.Log()
	if len(log) != 2 {
		t.Fatalf("want 2 events, got %d: %v", len(log), names(log))
	}
	if log[0].Kind != TimeoutEvent || log[1].Kind != NormalEvent {
		t.Fatalf("timeout must precede normal event at the same instant, got %v", kinds(log))
	}
	if log[0].At != 5 || log[1].At != 5 {
		t.Fatalf("both events must be at tick 5, got %d,%d", log[0].At, log[1].At)
	}
	if order[0] != TimeoutEvent || order[1] != NormalEvent {
		t.Fatalf("handler observed wrong order: %v", order)
	}
}

// TestZeroStepsDoNotAdvanceClock 验证普通事件不消耗时间，只有显式推进才改时钟。
func TestZeroStepsDoNotAdvanceClock(t *testing.T) {
	s := NewScheduler(HandlerFunc(func(ev Event) {}), 8)
	s.Post("a")
	s.Post("b")
	if s.Clock() != 0 {
		t.Fatalf("post must not move clock, got %d", s.Clock())
	}
	if err := s.Advance(0); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if s.Clock() != 0 {
		t.Fatalf("zero-duration events must keep clock at 0, got %d", s.Clock())
	}
	if err := s.Advance(3); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if s.Clock() != 3 {
		t.Fatalf("clock should be 3 after explicit advance, got %d", s.Clock())
	}
}

// TestLivelockBudgetExhausted 验证连续零耗时步数触顶后返回 ErrLivelock，
// 且未到期的超时仍被保留，不会被偷偷派发。
func TestLivelockBudgetExhausted(t *testing.T) {
	var s *Scheduler
	s = NewScheduler(HandlerFunc(func(ev Event) {
		if ev.Kind == NormalEvent {
			// 每条普通消息都再生产一条普通消息，模拟无限自我推迟。
			s.Post("self-perpetuating")
		}
	}), 4)
	s.After("deadline", 10)
	s.Post("seed")

	err := s.Advance(0)
	if !errors.Is(err, ErrLivelock) {
		t.Fatalf("want ErrLivelock, got %v", err)
	}
	if s.Clock() != 0 {
		t.Fatalf("clock must stay frozen at 0, got %d", s.Clock())
	}
	if s.ZeroUsed() != 4 {
		t.Fatalf("want 4 zero steps used, got %d", s.ZeroUsed())
	}
	var hasDeadline bool
	for _, ev := range s.Pending() {
		if ev.Name == "deadline" && ev.Kind == TimeoutEvent {
			hasDeadline = true
		}
	}
	if !hasDeadline {
		t.Fatal("pending deadline must survive the livelock so it can still fire once time advances")
	}
}

// TestPersistentRetransmit 用小模型验证持续重传：整体截止前按固定间隔
// 不断重传，截止时刻拒绝会话并停止重传。
func TestPersistentRetransmit(t *testing.T) {
	w := NewWorld(2, Config{Deadline: 10, RetransmitInterval: 3}, false)
	w.Start()

	// tick=0：首个 auth.request 零耗时派发后静止。
	if err := w.Scheduler().Advance(0); err != nil {
		t.Fatalf("drain tick0: %v", err)
	}
	if w.Session().Sent() != 1 {
		t.Fatalf("initial request sent once, got %d", w.Session().Sent())
	}

	// 逐 tick 推进到截止，重传应在 3、6、9 各发生一次。
	for tick := Time(1); tick <= 10; tick++ {
		if err := w.Scheduler().Advance(1); err != nil {
			t.Fatalf("advance to %d: %v", tick, err)
		}
	}

	if w.Session().State() != Rejected {
		t.Fatalf("session must be rejected at deadline, got %s", w.Session().State())
	}
	// 1 次初始请求 + 3 次重传（3、6、9）；tick=10 的截止不再产生重传。
	if w.Session().Sent() != 4 {
		t.Fatalf("want 4 sends (1 request + 3 retransmits), got %d", w.Session().Sent())
	}

	// 截止之后再推进时间也不会有任何重传。
	sent := w.Session().Sent()
	if err := w.Scheduler().Advance(5); err != nil {
		t.Fatalf("post-deadline advance: %v", err)
	}
	if w.Session().Sent() != sent {
		t.Fatalf("retransmission must stop after rejection, got %d -> %d", sent, w.Session().Sent())
	}
}

// TestRetransmitUntilResponse 验证在截止前收到合法响应会接受会话并停止定时器。
func TestRetransmitUntilResponse(t *testing.T) {
	w := NewWorld(2, Config{Deadline: 10, RetransmitInterval: 3}, false)
	w.Start()
	if err := w.Scheduler().Advance(0); err != nil {
		t.Fatalf("drain tick0: %v", err)
	}
	// 走到 tick 4：已经历 tick3 的一次重传。
	if _, _, err := w.Scheduler().AdvanceToNext(); err != nil {
		t.Fatalf("advance to retransmit: %v", err)
	}
	if w.Scheduler().Clock() != 3 {
		t.Fatalf("first retransmit at tick 3, got %d", w.Scheduler().Clock())
	}
	w.Respond()
	if _, _, err := w.Scheduler().AdvanceToNext(); err != nil {
		t.Fatalf("drain response: %v", err)
	}
	if w.Session().State() != Accepted {
		t.Fatalf("session must accept valid response, got %s", w.Session().State())
	}
	// 接受后不存在任何存活定时器。
	for _, ev := range w.Scheduler().Pending() {
		if ev.Kind == TimeoutEvent {
			t.Fatalf("no timer may remain after acceptance, found %s", ev.Name)
		}
	}
}

// TestInfiniteZeroCycleReflectedJunk 搜索由反射型无关消息构成的无限
// 零耗时循环，并断言返回可复现的循环证据，且时钟全程冻结。
func TestInfiniteZeroCycleReflectedJunk(t *testing.T) {
	// 预算给得很大：真正先触发的应是指纹重复，而不是预算耗尽。
	w := NewWorld(1_000_000, Config{Deadline: 10, RetransmitInterval: 3}, true)
	w.Start()
	w.InjectJunk()

	// tick=0 先派发初始请求，随后进入 junk 自再生循环。
	ev, err := SearchZeroCycle(w.Scheduler(), w.Fingerprint)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if ev == nil {
		t.Fatal("expected a zero-duration cycle from reflected junk, got none")
	}
	if ev.FrozenClock != 0 {
		t.Fatalf("cycle must freeze the clock at tick 0, got %d", ev.FrozenClock)
	}
	if len(ev.Loop) == 0 {
		t.Fatal("cycle evidence must contain at least one loop step")
	}
	for _, st := range ev.Loop {
		if st.Event.Kind != NormalEvent || st.Event.Name != EvJunk {
			t.Fatalf("loop body must consist of zero-duration junk, got %s", st.Event.Name)
		}
	}
	if ev.Loop[0].From != ev.LoopStartAt {
		t.Fatal("loop entry fingerprint mismatch")
	}
	if ev.Loop[len(ev.Loop)-1].To != ev.LoopStartAt {
		t.Fatal("loop must close back onto its entry fingerprint")
	}
	if w.Scheduler().Clock() != 0 {
		t.Fatalf("search must never advance time, got %d", w.Scheduler().Clock())
	}
	t.Logf("\n%s", ev.String())
}

// TestSameCycleCaughtByBudget 验证同一条无限零耗时链在显式预算下
// 也会被 MaxZeroSteps 截断（两种手段互相印证）。
func TestSameCycleCaughtByBudget(t *testing.T) {
	w := NewWorld(3, Config{Deadline: 10, RetransmitInterval: 3}, true)
	w.Start()
	w.InjectJunk()
	err := w.Scheduler().Advance(0)
	if !errors.Is(err, ErrLivelock) {
		t.Fatalf("reflected junk must exhaust the zero-step budget, got %v", err)
	}
	if w.Session().State() == Rejected {
		t.Fatal("deadline must not have been reached while clock is frozen")
	}
}

// TestBoundedUnfoldReachesDeadline 对显式允许的最大零时步数作展开：
// 无反射时零耗时链有限，系统能够走到静止并继续推进时间，
// 最终在截止时刻拒绝会话（活性：该拒绝的会话不会被永久保留）。
func TestBoundedUnfoldReachesDeadline(t *testing.T) {
	const maxZero = uint64(4)
	w := NewWorld(maxZero, Config{Deadline: 6, RetransmitInterval: 2}, false)
	w.Start()
	// 在 tick 0 注入若干有限的无关消息，数量低于预算。
	w.InjectJunk()
	w.InjectJunk()

	for tick := Time(1); tick <= 6; tick++ {
		if err := w.Scheduler().Advance(1); err != nil {
			t.Fatalf("advance to %d: %v", tick, err)
		}
		if w.Scheduler().ZeroUsed() > maxZero {
			t.Fatalf("zero-step budget exceeded at tick %d: %d", tick, w.Scheduler().ZeroUsed())
		}
	}
	if w.Session().State() != Rejected {
		t.Fatalf("bounded unfolding must still reach rejection, got %s", w.Session().State())
	}
	if w.Session().JunkDropped() != 2 {
		t.Fatalf("both junk messages must have been drained, got %d", w.Session().JunkDropped())
	}
}

// TestNoCycleWhenQuiescent 验证当前时刻走到静止时搜索报告“无循环”。
func TestNoCycleWhenQuiescent(t *testing.T) {
	w := NewWorld(8, Config{Deadline: 10, RetransmitInterval: 3}, false)
	w.Start()
	ev, err := SearchZeroCycle(w.Scheduler(), w.Fingerprint)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if ev != nil {
		t.Fatalf("finite zero-step prefix must not be reported as a cycle: %s", ev.String())
	}
}

// TestModelDeadlineWinsAtExpiry 用小模型验证“到期同刻”：在截止时刻
// 排着一条普通零耗时消息时，截止超时仍然先派发并拒绝会话，
// 普通事件无法把本应拒绝的会话挽留下来。
func TestModelDeadlineWinsAtExpiry(t *testing.T) {
	w := NewWorld(8, Config{Deadline: 5, RetransmitInterval: 2}, false)
	w.Start()

	// 自定义处理器：在截止前最后一次重传（tick=4）时，锚定一条在 tick=5
	// 派发的零耗时普通消息，使它与截止超时同刻竞争。
	base := HandlerFunc(w.Handle)
	w.Scheduler().handler = HandlerFunc(func(ev Event) {
		base.Handle(ev)
		if ev.Name == EvRetransmit && w.Scheduler().Clock() == 4 {
			w.Scheduler().PostAt(EvJunk, 5)
		}
	})

	for tick := Time(1); tick <= 5; tick++ {
		if err := w.Scheduler().Advance(1); err != nil {
			t.Fatalf("advance to %d: %v", tick, err)
		}
	}

	log := w.Scheduler().Log()
	last := log[len(log)-2:]
	if last[0].Name != EvDeadline || last[0].Kind != TimeoutEvent || last[0].At != 5 {
		t.Fatalf("deadline must be delivered first at tick 5, got %+v", last[0])
	}
	if last[1].Name != EvJunk || last[1].Kind != NormalEvent || last[1].At != 5 {
		t.Fatalf("junk must be delivered after the timeout at tick 5, got %+v", last[1])
	}
	if w.Session().State() != Rejected {
		t.Fatalf("session must be rejected even with a queued message, got %s", w.Session().State())
	}
}

// TestAdvanceToNextJumpsOnlyToTimeouts 验证 AdvanceToNext 只锚定超时事件，
// 零耗时事件不会被当作时间锚点。
func TestAdvanceToNextJumpsOnlyToTimeouts(t *testing.T) {
	var s *Scheduler
	s = NewScheduler(HandlerFunc(func(ev Event) {
		if ev.Kind == TimeoutEvent && ev.Name == "first" {
			s.After("second", s.Clock()+2)
		}
	}), 4)
	s.Post("warmup")
	s.After("first", 3)

	reached, ok, err := s.AdvanceToNext()
	if err != nil || !ok || reached != 3 || s.Clock() != 3 {
		t.Fatalf("first jump must land on tick 3, got reached=%d ok=%v err=%v", reached, ok, err)
	}
	reached, ok, err = s.AdvanceToNext()
	if err != nil || !ok || reached != 5 {
		t.Fatalf("chained timeout must land on tick 5, got reached=%d ok=%v err=%v", reached, ok, err)
	}
	if s.Clock() != 5 {
		t.Fatalf("clock must be 5, got %d", s.Clock())
	}
}
