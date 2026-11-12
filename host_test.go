package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"example.org/netsec/q0380"
)

// 收窄防线：后装表若在有限域内把任何键的最终动作放宽，Install 必须在调用任何
// 挂载钩子前返回 WideningError，且宿主链与全部线程链保持安装前状态。
func TestWideningRejectedBeforeAnySideEffect(t *testing.T) {
	var hookCalls atomic.Int32
	host, err := netsec.NewHost(testDomain(t), func(*netsec.Thread, *netsec.Chain) error {
		hookCalls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, err := host.AddThread(id); err != nil {
			t.Fatal(err)
		}
	}

	// 第一张表把整个有限域的默认动作收窄为 Deny。
	strict := mustTable(t, "strict", nil, netsec.Decision{Action: netsec.Deny, Tag: "deny-all"})
	if err := host.Install(strict); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	if hookCalls.Load() != 3 {
		t.Fatalf("initial install hook calls: got %d want 3", hookCalls.Load())
	}

	domain := testDomain(t)
	// 后装表对一个原本走默认 Deny 的键给出显式 Allow：该键从兜底层升入显式层，
	// 最终动作由 Deny 降为 Allow，属于必须拦截的放宽。
	request := domain.Enumerate()[0]
	loose := mustTable(t, "loose", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Allow, Tag: "explicit-allow"},
	}, netsec.Decision{Action: netsec.Allow, Tag: "allow-rest"})

	if key, before, after, ok := host.Chain().IsNarrowing(domain, loose); ok {
		t.Fatalf("IsNarrowing must detect relaxation, got ok on key=%+v", key)
	} else if key != request || before.Action != netsec.Deny || after.Action != netsec.Allow {
		t.Fatalf("unexpected widening detail: key=%+v before=%s after=%s", key, before.Action, after.Action)
	}

	err = host.Install(loose)
	var widening *netsec.WideningError
	if !errors.As(err, &widening) {
		t.Fatalf("want WideningError, got %v", err)
	}
	if widening.Request != request ||
		widening.Before.Action != netsec.Deny ||
		widening.After.Action != netsec.Allow {
		t.Fatalf("unexpected widening error detail: %+v", widening)
	}
	// 拒绝放宽时不得有任何额外挂载调用，链长度与全部线程决策保持安装前状态。
	if hookCalls.Load() != 3 {
		t.Fatalf("rejected install must trigger no hook, total calls got %d want 3", hookCalls.Load())
	}
	if host.Chain().Len() != 1 {
		t.Fatalf("chain length must remain 1, got %d", host.Chain().Len())
	}
	thread, _ := host.Thread("t2")
	for _, key := range domain.Enumerate() {
		got := thread.Evaluate(key)
		if got.Action != netsec.Deny {
			t.Fatalf("thread %s relaxed %+v to %s after rejected install", thread.ID(), key, got.Action)
		}
	}
}

// 持平（同严格度、不同载荷）与进一步收窄都允许安装。
// IsNarrowing 是安装前的有限域穷举防线，直接验证它对“收窄/持平”放行、
// 并能正确返回每个键的新旧严格度对比结果。
func TestIsNarrowingGuardAcrossFiniteDomain(t *testing.T) {
	domain := testDomain(t)
	keys := domain.Enumerate()

	// 基线空链对所有键都是 Allow；追加默认 Deny 表在每个键上都是收窄。
	denyAll := mustTable(t, "deny-all", nil, netsec.Decision{Action: netsec.Deny})
	chain, err := netsec.NewChain().Append(denyAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := netsec.NewChain().IsNarrowing(domain, denyAll); !ok {
		t.Fatal("deny-all over allow baseline must be narrowing on every enumerated key")
	}

	// 再追加默认 Kill 表，仍然只收窄不降。
	killAll := mustTable(t, "kill-all", nil, netsec.Decision{Action: netsec.Kill})
	if key, before, after, ok := chain.IsNarrowing(domain, killAll); !ok {
		t.Fatalf("kill over deny must be narrowing, got key=%+v before=%s after=%s", key, before.Action, after.Action)
	}

	// 穷举校验结果必须与逐键 Resolve 的严格度比较完全一致。
	candidate, err := chain.Append(killAll)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		before, _ := chain.Resolve(key)
		after, _ := candidate.Resolve(key)
		if actionRank(after.Action) < actionRank(before.Action) {
			t.Fatalf("guard/resolve disagreement on %+v: %s -> %s", key, before.Action, after.Action)
		}
	}

	// 持平（同严格度不同载荷）也算收窄通过。
	payload := mustTable(t, "deny-payload", nil,
		netsec.Decision{Action: netsec.Deny, Errno: 7, Tag: "other"})
	if _, _, _, ok := chain.IsNarrowing(domain, payload); !ok {
		t.Fatal("equal-severity table must be accepted as non-relaxing")
	}
}

func TestEqualAndNarrowingInstallSucceed(t *testing.T) {
	host, err := netsec.NewHost(testDomain(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := host.AddThread("t1")
	if err != nil {
		t.Fatal(err)
	}
	request := mustKey(t, 2, 1, 20)

	deny := mustTable(t, "deny", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Deny, Errno: 1, Tag: "first"},
	}, netsec.Decision{Action: netsec.Allow})
	if err := host.Install(deny); err != nil {
		t.Fatal(err)
	}
	denyOtherPayload := mustTable(t, "deny2", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Deny, Errno: 9, Tag: "second"},
	}, netsec.Decision{Action: netsec.Allow})
	if err := host.Install(denyOtherPayload); err != nil {
		t.Fatalf("equal severity should be accepted: %v", err)
	}
	if got := thread.Evaluate(request); got.Errno != 9 || got.Tag != "second" {
		t.Fatalf("equal severity latest payload: got %+v", got)
	}

	kill := mustTable(t, "kill", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Kill, Signal: 6, Tag: "kill"},
	}, netsec.Decision{Action: netsec.Allow})
	if err := host.Install(kill); err != nil {
		t.Fatalf("narrowing install: %v", err)
	}
	if got := thread.Evaluate(request); got.Action != netsec.Kill {
		t.Fatalf("want kill, got %s", got.Action)
	}
}

// 全线程同步安装失败时不允许留下部分链：覆盖首线程、中间线程、末线程失败。
func TestAtomicInstallFailureLeavesNoPartialChain(t *testing.T) {
	const threadCount = 4
	for _, failAt := range []int{0, 2, threadCount - 1} {
		t.Run(fmt.Sprintf("fail-thread-%d", failAt), func(t *testing.T) {
			domain := testDomain(t)
			ids := make([]string, threadCount)
			for i := range threadCount {
				ids[i] = fmt.Sprintf("t%d", i)
			}

			hook := func(thread *netsec.Thread, next *netsec.Chain) error {
				if next.Len() == 2 && thread.ID() == ids[failAt] {
					return errors.New("simulated attach failure")
				}
				return nil
			}
			host, err := netsec.NewHost(domain, hook)
			if err != nil {
				t.Fatal(err)
			}
			threads := make([]*netsec.Thread, threadCount)
			for i, id := range ids {
				threads[i], err = host.AddThread(id)
				if err != nil {
					t.Fatal(err)
				}
			}

			base := mustTable(t, "base-notify", nil, netsec.Decision{Action: netsec.Notify})
			if err := host.Install(base); err != nil {
				t.Fatal(err)
			}

			strict := mustTable(t, "strict-deny", nil, netsec.Decision{Action: netsec.Deny})
			err = host.Install(strict)
			var installErr *netsec.InstallError
			if !errors.As(err, &installErr) {
				t.Fatalf("want InstallError, got %v", err)
			}
			if !installErr.RolledBack || installErr.FailedThread != ids[failAt] {
				t.Fatalf("unexpected install error: %+v", installErr)
			}

			// 宿主链与全部线程链必须仍是安装前长度 1 的链，无部分链残留。
			if host.Chain().Len() != 1 {
				t.Fatalf("host chain len = %d, want 1", host.Chain().Len())
			}
			sample := domain.Enumerate()
			for i, thread := range threads {
				if thread.Chain().Len() != 1 {
					t.Fatalf("thread %s chain len = %d, want 1 (partial chain)", ids[i], thread.Chain().Len())
				}
				for _, request := range sample {
					got := thread.Evaluate(request)
					want, _ := host.Chain().Resolve(request)
					if got != want {
						t.Fatalf("thread %s diverges on %+v: got %+v want %+v", ids[i], request, got, want)
					}
					if got.Action != netsec.Notify {
						t.Fatalf("thread %s leaked stricter chain on %+v: %s", ids[i], request, got.Action)
					}
				}
			}
		})
	}
}

// 回滚钩子本身失败时 InstallError 必须如实标记 RolledBack=false，不能谎报成功。
func TestRollbackFailureIsReported(t *testing.T) {
	domain := testDomain(t)
	var hookMu sync.Mutex
	attachCalls := 0
	host, err := netsec.NewHost(domain, func(thread *netsec.Thread, next *netsec.Chain) error {
		hookMu.Lock()
		attachCalls++
		call := attachCalls
		hookMu.Unlock()
		switch {
		// 第 2 次安装（链长变为 2）：t0 为第 3 次调用并成功，t1 为第 4 次调用时失败。
		case next.Len() == 2 && call == 4:
			return errors.New("attach boom")
		// 随后对 t0 的回滚调用（恢复为链长 1）。
		case next.Len() == 1 && call == 5 && thread.ID() == "t0":
			return errors.New("rollback boom")
		default:
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.AddThread("t0"); err != nil {
		t.Fatal(err)
	}
	if _, err := host.AddThread("t1"); err != nil {
		t.Fatal(err)
	}
	if err := host.Install(mustTable(t, "base", nil, netsec.Decision{Action: netsec.Allow})); err != nil {
		t.Fatal(err)
	}
	err = host.Install(mustTable(t, "next", nil, netsec.Decision{Action: netsec.Notify}))
	var installErr *netsec.InstallError
	if !errors.As(err, &installErr) || installErr.RolledBack {
		t.Fatalf("want RolledBack=false, got %v", err)
	}
}
