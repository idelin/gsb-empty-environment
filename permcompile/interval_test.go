package permcompile

import (
	"errors"
	"testing"
)

func TestIntervalSetCarve(t *testing.T) {
	s := newIntervalSet(0, 100)

	if !s.containsRange(0, 100) {
		t.Fatal("full range should be covered")
	}
	if s.containsRange(50, 120) {
		t.Fatal("out-of-scope range must not be covered")
	}

	mid, ok := s.carve(40, 60)
	if !ok {
		t.Fatal("middle carve failed")
	}
	if got := mid.key(); got != "[0:40][60:100]" {
		t.Fatalf("middle carve = %s", got)
	}

	left, ok := mid.carve(0, 40)
	if !ok {
		t.Fatal("left carve failed")
	}
	if got := left.key(); got != "[60:100]" {
		t.Fatalf("left carve = %s", got)
	}

	if _, ok := left.carve(70, 90); !ok {
		t.Fatal("right carve failed")
	}
	if _, ok := left.carve(0, 10); ok {
		t.Fatal("carving uncovered range must fail")
	}

	// 原集合不被修改，无别名。
	if s.key() != "[0:100]" {
		t.Fatalf("original mutated: %s", s.key())
	}
}

func TestServeCallbackAtomicity(t *testing.T) {
	plan := &Plan{Root: leafGrant("o", 1, 0, 10, 1)}
	cp, err := Compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(cp, "s", func(Event) bool { return true })
	rq := req("o", 1, 0, 10)

	called := 0
	boomed := errors.New("callback failure")
	d, err := rt.Serve(rq, func(ReadRequest) error {
		called++
		return boomed
	})
	if !errors.Is(err, boomed) || d.Verdict != VerdictAllow {
		t.Fatalf("serve = %+v, %v", d, err)
	}
	// 回调失败不提交：配额保持，仍可再次服务。
	if got := rt.Snapshot()[0].Leaves[0].Reads.key(); got != "[0:1]" {
		t.Fatalf("quota changed after failed callback: %s", got)
	}

	d, err = rt.Serve(rq, func(ReadRequest) error {
		called++
		return nil
	})
	if err != nil || d.Verdict != VerdictAllow {
		t.Fatalf("successful serve = %+v, %v", d, err)
	}
	if called != 2 {
		t.Fatalf("callback called %d times", called)
	}
	if got := rt.Snapshot()[0].Leaves[0].Reads.key(); got != "E" {
		t.Fatalf("quota not committed: %s", got)
	}

	// 次数耗尽后拒绝。
	d, _ = rt.Serve(rq, func(ReadRequest) error { return nil })
	if d.Verdict != VerdictDeny {
		t.Fatalf("exhausted serve = %+v", d)
	}
}
