package netsec_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"example.org/netsec/q0380"
)

// 并发安装与评估期间，任何线程都读不到中间状态；每次安装后所有线程立即共享同一链。
func TestConcurrentInstallAndEvaluateConsistency(t *testing.T) {
	domain := testDomain(t)
	host, err := netsec.NewHost(domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	const threadCount = 6
	threads := make([]*netsec.Thread, threadCount)
	for i := range threadCount {
		threads[i], err = host.AddThread(fmt.Sprintf("t%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	keys := domain.Enumerate()

	// 默认决策逐级收窄：Allow -> Notify -> Deny -> Kill，始终满足收窄约束。
	defaults := []netsec.Action{netsec.Notify, netsec.Deny, netsec.Kill}
	var installed atomic.Int32

	var evaluators sync.WaitGroup
	stop := make(chan struct{})
	for _, thread := range threads {
		evaluators.Add(1)
		go func(thread *netsec.Thread) {
			defer evaluators.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for _, key := range keys {
						decision := thread.Evaluate(key)
						if !decision.Action.Valid() {
							t.Errorf("invalid action observed: %d", decision.Action)
							return
						}
					}
				}
			}
		}(thread)
	}

	for _, action := range defaults {
		table := mustTable(t, "level-"+action.String(), nil, netsec.Decision{Action: action})
		if err := host.Install(table); err != nil {
			t.Fatalf("install %s: %v", action, err)
		}
		installed.Add(1)

		wantLen := host.Chain().Len()
		for _, thread := range threads {
			if thread.Chain().Len() != wantLen {
				t.Fatalf("thread %s chain len %d, host len %d", thread.ID(), thread.Chain().Len(), wantLen)
			}
		}
	}
	close(stop)
	evaluators.Wait()

	if installed.Load() != int32(len(defaults)) {
		t.Fatalf("installed count: got %d want %d", installed.Load(), len(defaults))
	}
	for _, key := range keys {
		for _, thread := range threads {
			if got := thread.Evaluate(key); got.Action != netsec.Kill {
				t.Fatalf("thread %s final action on %+v: got %s want kill", thread.ID(), key, got.Action)
			}
		}
	}
}

// 随机生成判定表并在有限域上穷举：成功安装的最终动作严格度单调不降；
// 被拒安装不改变任何线程状态。
func TestRandomTablesRespectMonotonicNarrowing(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261112, 42))
	domain := testDomain(t)
	keys := domain.Enumerate()
	actions := []netsec.Action{netsec.Allow, netsec.Notify, netsec.Deny, netsec.Kill}

	for iteration := range 200 {
		host, err := netsec.NewHost(domain, nil)
		if err != nil {
			t.Fatal(err)
		}
		const threadCount = 3
		threads := make([]*netsec.Thread, threadCount)
		for i := range threadCount {
			threads[i], err = host.AddThread(fmt.Sprintf("iter%d-t%d", iteration, i))
			if err != nil {
				t.Fatal(err)
			}
		}

		currentRank := make(map[netsec.Key]int, len(keys))
		for _, key := range keys {
			currentRank[key] = actionRank(netsec.Allow)
		}

		for step := range 6 {
			rules := make(map[netsec.Key]netsec.Decision)
			for _, key := range keys {
				if rng.IntN(2) == 0 {
					continue
				}
				action := actions[rng.IntN(len(actions))]
				rules[key] = netsec.Decision{
					Action: action,
					Errno:  uint16(rng.IntN(64)),
					Tag:    fmt.Sprintf("r%d-s%d", iteration, step),
				}
			}
			table := mustTable(t, fmt.Sprintf("random-%d-%d", iteration, step), rules,
				netsec.Decision{Action: actions[rng.IntN(len(actions))]})

			beforeLen := host.Chain().Len()
			err := host.Install(table)
			if err != nil {
				var widening *netsec.WideningError
				if !errors.As(err, &widening) {
					t.Fatalf("iteration %d step %d: unexpected error %v", iteration, step, err)
				}
				if host.Chain().Len() != beforeLen {
					t.Fatalf("rejected install changed chain length: %d -> %d", beforeLen, host.Chain().Len())
				}
				continue
			}

			for _, key := range keys {
				decision, _ := host.Chain().Resolve(key)
				gotRank := actionRank(decision.Action)
				if gotRank < currentRank[key] {
					t.Fatalf("iteration %d step %d key %+v widened rank %d -> %d",
						iteration, step, key, currentRank[key], gotRank)
				}
				currentRank[key] = gotRank
				for _, thread := range threads {
					if thread.Evaluate(key) != decision {
						t.Fatalf("iteration %d step %d thread %s diverges on %+v", iteration, step, thread.ID(), key)
					}
				}
			}
		}
	}
}

// 判定表构造后不可变：调用方修改原始 map 不影响已构造的表。
func TestTableIsImmutableAfterConstruction(t *testing.T) {
	key := mustKey(t, 1, 0, 10)
	rules := map[netsec.Key]netsec.Decision{
		key: {Action: netsec.Deny, Tag: "original"},
	}
	table := mustTable(t, "immutable", rules, netsec.Decision{Action: netsec.Allow})

	rules[key] = netsec.Decision{Action: netsec.Allow, Tag: "mutated"}
	if got, _ := table.Lookup(key); got.Action != netsec.Deny || got.Tag != "original" {
		t.Fatalf("table must be immune to caller map mutation, got %+v", got)
	}

	snapshot := table.Rules()
	snapshot[key] = netsec.Decision{Action: netsec.Kill, Tag: "snapshot-mutated"}
	if got, _ := table.Lookup(key); got.Action != netsec.Deny {
		t.Fatalf("Rules snapshot must be detached from table, got %+v", got)
	}
}
