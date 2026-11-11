package netsec

import (
	"errors"
	"testing"
)

func intSet(xs []int) map[int]bool {
	m := make(map[int]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// 双方交叉等待：A 等 rB（由 B 的接收产出），B 等 rA（由 A 的接收产出），
// 而两个接收各自依赖对方被阻塞后才会发生的发送，构成最小等待环。
func crossDeadlockEvents() []Event {
	return []Event{
		// A 等 rB：rB 只能由 B 接收 A 的消息 mA 后产出；
		// B 等 rA：rA 只能由 A 接收 B 的消息 mB 后产出。对称跨方，构成等待环。
		{Kind: KindWait, Actor: "A", Resources: []string{"rB"}},    // 0 锚点
		{Kind: KindWait, Actor: "B", Resources: []string{"rA"}},    // 1 锚点
		{Kind: KindSend, Actor: "A", Ref: "mA"},                    // 2 A 已阻塞，被跳过
		{Kind: KindReceive, Actor: "B", Ref: "mA", Produces: "rB"}, // 3 B 已阻塞，被跳过
		{Kind: KindSend, Actor: "B", Ref: "mB"},                    // 4 B 已阻塞，被跳过
		{Kind: KindReceive, Actor: "A", Ref: "mB", Produces: "rA"}, // 5 A 已阻塞，被跳过
	}
}

func TestAnalyzeCrossDeadlock(t *testing.T) {
	rep, err := Analyze(crossDeadlockEvents(), Options{})
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if rep.TotalSubsets != 1<<6 {
		t.Fatalf("TotalSubsets = %d, want %d", rep.TotalSubsets, 1<<6)
	}
	if rep.ReplayCount != 1<<6 {
		t.Fatalf("ReplayCount = %d, want every subset replayed", rep.ReplayCount)
	}
	if got := intSet(rep.AnchorWaits); !got[0] || !got[1] || len(got) != 2 {
		t.Fatalf("AnchorWaits = %v, want [0 1]", rep.AnchorWaits)
	}
	if len(rep.Cores) == 0 {
		t.Fatal("expected at least one minimal core")
	}
	core := rep.Cores[0]
	if core.Trivial {
		t.Fatal("cross-deadlock core must not be trivial")
	}
	got := intSet(core.Events)
	for _, want := range []int{0, 1} {
		if !got[want] {
			t.Fatalf("core %v missing anchor #%d", core.Events, want)
		}
	}
	// 最小子图必须包含至少一个真实消息/生产事件，且全部锚点仍卡死。
	if len(core.Events) <= 2 {
		t.Fatalf("core %v should carry a real message dependency", core.Events)
	}
	if !reproducesStall(core.Replay, rep.FullReplay) {
		t.Fatalf("core replay pending %v does not cover anchors %v", core.Replay.Pending, rep.AnchorWaits)
	}
	for _, chk := range core.RemovalChecks {
		if chk.StillStuck {
			t.Fatalf("removing #%d unexpectedly keeps every anchor stuck", chk.RemovedIndex)
		}
		if len(chk.Released) == 0 && len(chk.SkippedLost) == 0 {
			t.Fatalf("removal check for #%d has no necessity evidence", chk.RemovedIndex)
		}
	}
	if len(core.RemovalChecks) != len(core.Events)-len(rep.AnchorWaits) {
		t.Fatalf("every non-anchor event needs a removal check, got %v", core.RemovalChecks)
	}
	// 子图必须严格按真实顺序排列。
	for i := 1; i < len(core.Events); i++ {
		if core.Events[i-1] >= core.Events[i] {
			t.Fatalf("core events not in real order: %v", core.Events)
		}
	}
	if !rep.RingClosed {
		t.Fatalf("expected a closed wait ring, got %v", rep.Ring)
	}
}

func TestTrivialAnchorOnlyCore(t *testing.T) {
	events := []Event{
		{Kind: KindWait, Actor: "A", Resources: []string{"r"}}, // 0 资源从未生产
		{Kind: KindProduce, Actor: "B", Produces: "other"},     // 1 无关
	}
	rep, err := Analyze(events, Options{})
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	core := rep.Cores[0]
	if !core.Trivial || len(core.Events) != 1 || core.Events[0] != 0 {
		t.Fatalf("want trivial anchor-only core {0}, got %v trivial=%v", core.Events, core.Trivial)
	}
	if len(core.RemovalChecks) != 0 {
		t.Fatalf("trivial core must have no non-anchor removal checks, got %v", core.RemovalChecks)
	}
}

// 择一满足：等待 r1 或 r2；其中一个分支可被生产时，等待立即解除，不再卡死。
func TestOrSatisfactionReleasesWait(t *testing.T) {
	events := []Event{
		{Kind: KindProduce, Actor: "B", Produces: "r2"},               // 0
		{Kind: KindWait, Actor: "A", Resources: []string{"r1", "r2"}}, // 1 立即满足
	}
	_, err := Analyze(events, Options{})
	if !errors.Is(err, ErrNoPendingWaits) {
		t.Fatalf("want ErrNoPendingWaits, got %v", err)
	}
	rep, err := ReplaySubset(events, []int{1})
	if err != nil {
		t.Fatalf("ReplaySubset error: %v", err)
	}
	if len(rep.Pending) != 1 || rep.Pending[0] != 1 {
		t.Fatalf("wait without producer should be pending, got %v", rep.Pending)
	}
}

// 取消：等待被真实取消后不再计入当前等待。
func TestCancelClearsWait(t *testing.T) {
	events := []Event{
		{Kind: KindWait, Actor: "A", Resources: []string{"r"}}, // 0
		{Kind: KindCancel, Actor: "A", Ref: WaitRef(0)},        // 1
	}
	rep, err := ReplaySubset(events, []int{0, 1})
	if err != nil {
		t.Fatalf("ReplaySubset error: %v", err)
	}
	if len(rep.Pending) != 0 {
		t.Fatalf("cancelled wait must not remain pending, got %v", rep.Pending)
	}
	_, err = Analyze(events, Options{})
	if !errors.Is(err, ErrNoPendingWaits) {
		t.Fatalf("want ErrNoPendingWaits after cancel, got %v", err)
	}
}

// 不补造消息：只有接收、没有真实发送时接收必须卡死；加入真实发送后解除。
func TestNoFabricatedMessage(t *testing.T) {
	events := []Event{
		{Kind: KindReceive, Actor: "A", Ref: "m", Produces: "r"}, // 0 无发送
		{Kind: KindWait, Actor: "B", Resources: []string{"r"}},   // 1 因 A 阻塞而卡死
		{Kind: KindSend, Actor: "C", Ref: "m"},                   // 2 真实发送
	}
	// 去掉发送后：0、1 都应处于等待。
	stuck, err := ReplaySubset(events, []int{0, 1})
	if err != nil {
		t.Fatalf("ReplaySubset error: %v", err)
	}
	if got := intSet(stuck.Pending); !got[0] || !got[1] {
		t.Fatalf("without send, want pending [0 1], got %v", stuck.Pending)
	}
	// 真实发送发生在接收之后：接收时刻消息尚不存在，不能回溯补造，因此仍卡死。
	full, err := ReplaySubset(events, []int{0, 1, 2})
	if err != nil {
		t.Fatalf("ReplaySubset error: %v", err)
	}
	if got := intSet(full.Pending); !got[0] || !got[1] {
		t.Fatalf("late send must not retroactively unblock receive, got pending %v", full.Pending)
	}
	// 提供一个先于接收的真实发送时，消息来自真实事件，等待链才解除。
	ordered := []Event{
		{Kind: KindSend, Actor: "C", Ref: "m"},
		{Kind: KindReceive, Actor: "A", Ref: "m", Produces: "r"},
		{Kind: KindWait, Actor: "B", Resources: []string{"r"}},
	}
	ok, err := ReplaySubset(ordered, []int{0, 1, 2})
	if err != nil {
		t.Fatalf("ReplaySubset error: %v", err)
	}
	if len(ok.Pending) != 0 {
		t.Fatalf("real send before receive must unblock chain, got pending %v", ok.Pending)
	}
}

func TestRealOrderPreserved(t *testing.T) {
	events := crossDeadlockEvents()
	rep, err := Analyze(events, Options{})
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	for _, core := range rep.Cores {
		for i := 1; i < len(core.Replay.Included); i++ {
			if core.Replay.Included[i-1] > core.Replay.Included[i] {
				t.Fatalf("replay order violated: %v", core.Replay.Included)
			}
		}
	}
}

func TestTooManyCandidates(t *testing.T) {
	events := make([]Event, MaxCandidates+1)
	for i := range events {
		events[i] = Event{Kind: KindProduce, Actor: "A", Produces: "r"}
	}
	if _, err := Analyze(events, Options{}); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("want ErrTooManyCandidates, got %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]Event{
		"empty actor":      {Kind: KindWait, Resources: []string{"r"}},
		"wait no resource": {Kind: KindWait, Actor: "A"},
		"produce empty":    {Kind: KindProduce, Actor: "A"},
		"send no ref":      {Kind: KindSend, Actor: "A"},
		"cancel bad ref":   {Kind: KindCancel, Actor: "A", Ref: "nope"},
		"cancel other":     {Kind: KindCancel, Actor: "B", Ref: "0"},
	}
	base := crossDeadlockEvents()
	for name, bad := range cases {
		events := append(append([]Event{}, base...), bad)
		if _, err := Analyze(events, Options{}); err == nil {
			t.Fatalf("case %q: expected validation error", name)
		}
	}
}

func TestExhaustiveEnumerationAtLimit(t *testing.T) {
	// 20 个事件：前 19 个无关生产，第 20 个等待一个从未被生产的资源。
	events := make([]Event, 0, MaxCandidates)
	for i := 0; i < MaxCandidates-1; i++ {
		events = append(events, Event{Kind: KindProduce, Actor: "A", Produces: "p"})
	}
	events = append(events, Event{Kind: KindWait, Actor: "B", Resources: []string{"missing"}})
	rep, err := Analyze(events, Options{})
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if rep.TotalSubsets != 1<<MaxCandidates {
		t.Fatalf("TotalSubsets = %d, want %d", rep.TotalSubsets, 1<<MaxCandidates)
	}
	core := rep.Cores[0]
	if !core.Trivial || len(core.Events) != 1 || core.Events[0] != MaxCandidates-1 {
		t.Fatalf("want single trivial anchor core, got %v", core.Events)
	}
}
