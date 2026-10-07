package backtest

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// echoStrategy 在每条事件到达时读取目标 ID 的当前值并产生一个动作，
// 用于观察策略在每个决策点实际看到的值。
type echoStrategy struct{ watchID string }

func (s *echoStrategy) OnEvent(e Event, view *View) []Action {
	v, ok := view.Get(s.watchID)
	if !ok {
		return []Action{{Kind: "no-data"}}
	}
	return []Action{{Kind: "observe", Value: v.Value}}
}

func TestSameArrivalOrderedBySeq(t *testing.T) {
	// 三条事件同时到达，输入顺序与 Seq 相反，应按 Seq 升序处理。
	events := []Event{
		{Seq: 3, ID: "x", Version: 3, OccurAt: at(0), ArriveAt: at(10), Value: 30},
		{Seq: 1, ID: "x", Version: 1, OccurAt: at(0), ArriveAt: at(10), Value: 10},
		{Seq: 2, ID: "x", Version: 2, OccurAt: at(0), ArriveAt: at(10), Value: 20},
	}
	res, err := NewEngine(&echoStrategy{watchID: "x"}).Run(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{10, 20, 30}
	if len(res.Actions) != len(want) {
		t.Fatalf("got %d actions, want %d", len(res.Actions), len(want))
	}
	for i, a := range res.Actions {
		if a.Value != want[i] {
			t.Errorf("action #%d value=%v, want %v", i, a.Value, want[i])
		}
		if a.Seq != int64(i+1) {
			t.Errorf("action #%d seq=%d, want %d", i, a.Seq, i+1)
		}
	}
}

func TestNoFutureData(t *testing.T) {
	// "late" 在 t=100 才到达，t=10 时策略读它必须读不到。
	events := []Event{
		{Seq: 1, ID: "early", Version: 1, OccurAt: at(5), ArriveAt: at(10), Value: 1},
		{Seq: 2, ID: "late", Version: 1, OccurAt: at(5), ArriveAt: at(100), Value: 2},
	}
	res, err := NewEngine(&echoStrategy{watchID: "late"}).Run(events)
	if err != nil {
		t.Fatal(err)
	}
	if res.Actions[0].Kind != "no-data" {
		t.Errorf("at t=10 strategy saw future data: %+v", res.Actions[0])
	}
	if res.Actions[1].Kind != "observe" || res.Actions[1].Value != 2 {
		t.Errorf("at t=100 strategy should see value 2: %+v", res.Actions[1])
	}
	if err := res.Audit.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestLateCorrectionAffectsFutureNotHistory(t *testing.T) {
	// 原始值 100 在 t=10 到达；更正值 80 在 t=30 才到达（迟到更正）。
	// t=10 的决策必须用 100 且不可更改；t=30 起的决策必须用 80。
	events := []Event{
		{Seq: 1, ID: "px", Version: 1, OccurAt: at(1), ArriveAt: at(10), Value: 100},
		{Seq: 2, ID: "other", Version: 1, OccurAt: at(2), ArriveAt: at(20), Value: 0},
		{Seq: 3, ID: "px", Version: 2, OccurAt: at(1), ArriveAt: at(30), Value: 80},
		{Seq: 4, ID: "other2", Version: 1, OccurAt: at(3), ArriveAt: at(40), Value: 0},
	}
	res, err := NewEngine(&echoStrategy{watchID: "px"}).Run(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{100, 100, 80, 80}
	if len(res.Actions) != len(want) {
		t.Fatalf("got %d actions, want %d", len(res.Actions), len(want))
	}
	for i, a := range res.Actions {
		if a.Value != want[i] {
			t.Errorf("action #%d value=%v, want %v (history must not be rewritten)", i, a.Value, want[i])
		}
	}
	// 审计读集合中，t=10 的读取必须对应版本 1，t=40 的读取必须对应版本 2。
	reads := res.Audit.Reads
	if reads[0].Version != 1 || reads[0].Value != 100 {
		t.Errorf("first read = %+v, want version 1 value 100", reads[0])
	}
	last := reads[len(reads)-1]
	if last.Version != 2 || last.Value != 80 {
		t.Errorf("last read = %+v, want version 2 value 80", last)
	}
}

func TestReplayDeterministic(t *testing.T) {
	events := []Event{
		{Seq: 1, ID: "px", Version: 1, OccurAt: at(1), ArriveAt: at(10), Value: 100},
		{Seq: 2, ID: "px", Version: 2, OccurAt: at(1), ArriveAt: at(30), Value: 80},
		{Seq: 3, ID: "sig", Version: 1, OccurAt: at(2), ArriveAt: at(30), Value: 1},
	}
	res, err := Replay(NewEngine(&echoStrategy{watchID: "px"}), events)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 3 {
		t.Fatalf("got %d actions, want 3", len(res.Actions))
	}
}

func TestArriveBeforeOccurRejected(t *testing.T) {
	events := []Event{
		{Seq: 1, ID: "bad", Version: 1, OccurAt: at(10), ArriveAt: at(5), Value: 1},
	}
	if _, err := NewEngine(&echoStrategy{watchID: "bad"}).Run(events); err == nil {
		t.Fatal("expected error for event arriving before it occurs")
	}
}

func TestStaleVersionDoesNotRollback(t *testing.T) {
	// 版本 2 先于版本 1 到达（乱序更正），晚到的旧版本不得回滚已生效的新值。
	events := []Event{
		{Seq: 1, ID: "px", Version: 2, OccurAt: at(1), ArriveAt: at(10), Value: 80},
		{Seq: 2, ID: "px", Version: 1, OccurAt: at(1), ArriveAt: at(20), Value: 100},
		{Seq: 3, ID: "probe", Version: 1, OccurAt: at(2), ArriveAt: at(30), Value: 0},
	}
	res, err := NewEngine(&echoStrategy{watchID: "px"}).Run(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{80, 80, 80}
	for i, a := range res.Actions {
		if a.Value != want[i] {
			t.Errorf("action #%d value=%v, want %v", i, a.Value, want[i])
		}
	}
}
