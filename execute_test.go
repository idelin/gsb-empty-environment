package netsec

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

// simDevice 是模拟设备：每个块持有污点状态，共享资源边会传播污点，
// 复位清零对应块；可按需注入驱动故障。
type simDevice struct {
	spec      Spec
	taint     []bool
	clean     []bool
	failAt    map[int]bool
	commitErr error
	resetLog  []int
	committed bool
	aborted   bool
	isolated  bool
}

func newSimDevice(spec Spec) *simDevice {
	d := &simDevice{
		spec:     spec,
		taint:    make([]bool, len(spec.Blocks)),
		clean:    make([]bool, len(spec.Blocks)),
		failAt:   map[int]bool{},
		isolated: true,
	}
	for i, b := range spec.Blocks {
		d.taint[i] = b.Tainted
	}
	return d
}

func (d *simDevice) Reset(idx int) error {
	if d.failAt[idx] {
		return fmt.Errorf("block %d driver fault", idx)
	}
	d.taint[idx] = false
	d.clean[idx] = true
	d.resetLog = append(d.resetLog, idx)
	return nil
}

func (d *simDevice) Commit() error {
	if d.commitErr != nil {
		return d.commitErr
	}
	d.committed = true
	d.isolated = false
	return nil
}

func (d *simDevice) Abort() {
	d.aborted = true
	d.isolated = true
}

// leak 报告旧租户污点沿共享资源边传播后是否仍对新租户可观察。
// 已复位的块状态被清除，不再携带也不再传播旧租户状态。
func (d *simDevice) leak() bool {
	taint := append([]bool(nil), d.taint...)
	for changed := true; changed; {
		changed = false
		for _, e := range d.spec.Edges {
			if taint[e.From] && !taint[e.To] && !d.clean[e.To] {
				taint[e.To] = true
				changed = true
			}
		}
	}
	for i, b := range d.spec.Blocks {
		if b.Observable && taint[i] {
			return true
		}
	}
	return false
}

func TestExecuteSuccessOnSimDevice(t *testing.T) {
	r := rand.New(rand.NewSource(330))
	for trial := 0; trial < 100; trial++ {
		spec := randomSpec(r, 1+r.Intn(10))
		plan, err := PlanReset(spec)
		if err != nil {
			t.Fatal(err)
		}
		dev := newSimDevice(spec)
		if err := Execute(plan, dev); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if !slices.Equal(dev.resetLog, plan.Order) {
			t.Fatalf("trial %d: reset log %v != order %v", trial, dev.resetLog, plan.Order)
		}
		if !dev.committed || dev.aborted || dev.isolated {
			t.Fatalf("trial %d: bad final state %+v", trial, dev)
		}
		if dev.leak() {
			t.Fatalf("trial %d: old tenant state still observable after reset", trial)
		}
	}
}

func TestExecuteFailureKeepsIsolation(t *testing.T) {
	spec := Spec{
		Blocks: []Block{
			{Name: "src", Cost: 10, Tainted: true},
			{Name: "mid", Cost: 1},
			{Name: "snk", Cost: 10, Observable: true},
			{Name: "guard", Cost: 1},
		},
		Edges: []Edge{{0, 1}, {1, 2}},
		Pre:   [][]int{nil, {3}, nil, nil},
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	// 计划应为先 guard(3) 后 mid(1)；让 mid 复位失败。
	dev := newSimDevice(spec)
	dev.failAt[1] = true
	err = Execute(plan, dev)
	var stepErr *StepError
	if !errors.As(err, &stepErr) {
		t.Fatalf("expected *StepError, got %v", err)
	}
	if stepErr.Block != 1 || stepErr.Step != 1 {
		t.Fatalf("got %+v, want block 1 step 1", stepErr)
	}
	if !dev.aborted || dev.committed || !dev.isolated {
		t.Fatalf("device must stay isolated after failure: %+v", dev)
	}
	if !reflect.DeepEqual(dev.resetLog, []int{3}) {
		t.Fatalf("reset log %v, want only completed prefix [3]", dev.resetLog)
	}
	if !dev.leak() {
		t.Fatal("incomplete reset must still show leakage (isolation is what protects the new tenant)")
	}
}

func TestExecuteCommitFailureKeepsIsolation(t *testing.T) {
	spec := Spec{
		Blocks: []Block{
			{Name: "src", Cost: 1, Tainted: true},
			{Name: "snk", Cost: 1, Observable: true},
		},
		Edges: []Edge{{0, 1}},
		Pre:   make([][]int, 2),
	}
	plan, err := PlanReset(spec)
	if err != nil {
		t.Fatal(err)
	}
	dev := newSimDevice(spec)
	dev.commitErr = errors.New("handover channel down")
	if err := Execute(plan, dev); err == nil {
		t.Fatal("expected commit error")
	}
	if !dev.aborted || dev.committed || !dev.isolated {
		t.Fatalf("device must stay isolated when commit fails: %+v", dev)
	}
	if dev.leak() {
		t.Fatal("resets completed, no leakage expected")
	}
}

func TestExecuteEmptyPlan(t *testing.T) {
	dev := newSimDevice(Spec{})
	if err := Execute(Plan{}, dev); err != nil {
		t.Fatal(err)
	}
	if !dev.committed || dev.aborted {
		t.Fatalf("empty plan should commit directly: %+v", dev)
	}
}
