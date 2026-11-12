package netsec

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// ---- 可注入的解压器测试替身 ----

type scriptedStep struct {
	out  []byte
	cost Cost
	done bool
}

type scriptedDecompressor struct {
	steps     []scriptedStep
	pos       int
	nextCalls *int64
	closed    *int64
}

func (d *scriptedDecompressor) Next(ctx context.Context) (StepResult, error) {
	if err := ctx.Err(); err != nil {
		return StepResult{}, err
	}
	atomic.AddInt64(d.nextCalls, 1)
	if d.pos >= len(d.steps) {
		return StepResult{}, errors.New("scripted decompressor: steps exhausted")
	}
	s := d.steps[d.pos]
	d.pos++
	return StepResult{Output: s.out, Cost: s.cost, Done: s.done}, nil
}

func (d *scriptedDecompressor) Close() error {
	atomic.AddInt64(d.closed, 1)
	return nil
}

type scriptedFactory struct {
	steps       []scriptedStep
	instances   int64
	totalNext   int64
	totalClosed int64
}

func (f *scriptedFactory) NewDecompressor(_ []byte) (Decompressor, error) {
	atomic.AddInt64(&f.instances, 1)
	return &scriptedDecompressor{
		steps:     f.steps,
		nextCalls: &f.totalNext,
		closed:    &f.totalClosed,
	}, nil
}

// failAtLastStep 模拟“解压成功但在最后一步之后的片段校验才失败”的恶意片段。
func failAtLastStep() []scriptedStep {
	return []scriptedStep{
		{out: []byte("AAA"), cost: Cost{InputBytes: 10, OutputBytes: 20, DictOps: 2}},
		{out: []byte("BBB"), cost: Cost{InputBytes: 10, OutputBytes: 20, DictOps: 2}},
		{out: []byte("SECRET"), cost: Cost{InputBytes: 10, OutputBytes: 20, DictOps: 2}, done: true},
	}
}

type rejectingVerifier struct {
	calls int64
}

var errBadChecksum = errors.New("checksum mismatch")

func (v *rejectingVerifier) Verify(context.Context, []byte, []byte) error {
	atomic.AddInt64(&v.calls, 1)
	return errBadChecksum
}

// TestRetrySharedBudgetBoundedCPUNoLeak 是核心安全用例：
// 同一个授权请求反复重传在最后一步校验失败的片段，预算必须跨重试累计，
// 重试次数有界（不会无限消耗 CPU），且任何未认证输出字节都不泄漏给调用方。
func TestRetrySharedBudgetBoundedCPUNoLeak(t *testing.T) {
	factory := &scriptedFactory{steps: failAtLastStep()}
	verifier := &rejectingVerifier{}
	budget := NewBudget("req-1", Limits{
		// 每次完整尝试成本为 30 输入 / 60 输出 / 6 字典操作；
		// 上限恰好等于两次完整尝试的累计值，第三次尝试起只能走预检拒绝。
		MaxInputBytes:  60,
		MaxOutputBytes: 120,
		MaxDictOps:     12,
	})
	proc := NewProcessor(budget, factory, verifier)

	frag := Fragment{RequestID: "req-1", Payload: []byte("evil-payload")}

	// 第一次尝试：3 步全部解压，最后校验失败。
	out, err := proc.Process(context.Background(), frag)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("attempt 1: want ErrVerificationFailed, got %v", err)
	}
	if out != nil {
		t.Fatalf("attempt 1: unverified output leaked: %q", out)
	}

	// 第二次尝试：重试沿用同一预算，同样在最后一步校验失败。
	out, err = proc.Process(context.Background(), frag)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("attempt 2: want ErrVerificationFailed, got %v", err)
	}
	if out != nil {
		t.Fatalf("attempt 2: unverified output leaked: %q", out)
	}

	used := budget.Snapshot()
	if used.InputBytes != 60 || used.OutputBytes != 120 || used.DictOps != 12 {
		t.Fatalf("usage not accumulated across retries: %+v", used)
	}
	if used.Attempts != 2 {
		t.Fatalf("want 2 attempts, got %d", used.Attempts)
	}

	// 第三次尝试：预算已耗尽，预检直接拒绝，解压器一步都不会被驱动。
	nextBefore := atomic.LoadInt64(&factory.totalNext)
	out, err = proc.Process(context.Background(), frag)
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("attempt 3: want BudgetExceededError, got %v", err)
	}
	if out != nil {
		t.Fatalf("attempt 3: output must be nil, got %q", out)
	}
	if got := atomic.LoadInt64(&factory.totalNext); got != nextBefore {
		t.Fatalf("exhausted budget still drove decompressor: Next %d -> %d", nextBefore, got)
	}
	if budgetErr.Attempt != 3 {
		t.Fatalf("want attempt 3 on budget error, got %d", budgetErr.Attempt)
	}

	// 再多重试也只会走预检拒绝路径，CPU 消耗恒定有界。
	for i := 0; i < 100; i++ {
		out, err = proc.Process(context.Background(), frag)
		if !errors.As(err, &budgetErr) || out != nil {
			t.Fatalf("retry %d: must remain rejected without output, got %v %q", i, err, out)
		}
	}

	// 有界性断言：恰好 2 个完整尝试 × 3 步，第 3 次及以后仅预检拒绝。
	if got := atomic.LoadInt64(&factory.totalNext); got != 6 {
		t.Fatalf("want exactly 6 decompression steps, got %d", got)
	}
	if got := atomic.LoadInt64(&factory.instances); got != 2 {
		t.Fatalf("want 2 decompressor instances (exhausted retries allocate nothing), got %d", got)
	}
	if got := atomic.LoadInt64(&factory.totalClosed); got != 2 {
		t.Fatalf("want both decompressors closed, got %d closes", got)
	}
	if got := atomic.LoadInt64(&verifier.calls); got != 2 {
		t.Fatalf("verifier must run only after full decompression, calls=%d", got)
	}
}

// TestNewAuthorizedRequestResetsBudget 只有新的授权请求才重置预算。
func TestNewAuthorizedRequestResetsBudget(t *testing.T) {
	factory := &scriptedFactory{steps: failAtLastStep()}
	verifier := &rejectingVerifier{}
	budget := NewBudget("req-1", Limits{MaxInputBytes: 1000, MaxOutputBytes: 1000, MaxDictOps: 100})
	proc := NewProcessor(budget, factory, verifier)

	bad := Fragment{RequestID: "req-1", Payload: []byte("evil")}
	if _, err := proc.Process(context.Background(), bad); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("want verify failure, got %v", err)
	}
	if got := budget.Snapshot().InputBytes; got != 30 {
		t.Fatalf("want 30 input bytes charged, got %d", got)
	}

	// 相同 RequestID 的重试：预算保留，不会被刷新。
	if _, err := proc.Process(context.Background(), bad); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("want verify failure, got %v", err)
	}
	if got := budget.Snapshot().InputBytes; got != 60 {
		t.Fatalf("retry must accumulate, got %d", got)
	}

	// 新授权请求：预算清零，合法片段可以正常提交输出。
	goodFactory := &scriptedFactory{steps: failAtLastStep()}
	goodVerifier := VerifierFunc(func(_ context.Context, _, out []byte) error {
		if string(out) != "AAABBBSECRET" {
			return errors.New("unexpected isolated output")
		}
		return nil
	})
	goodProc := NewProcessor(budget, goodFactory, goodVerifier)
	out, err := goodProc.Process(context.Background(), Fragment{RequestID: "req-2", Payload: []byte("good")})
	if err != nil {
		t.Fatalf("new request should succeed, got %v", err)
	}
	if string(out) != "AAABBBSECRET" {
		t.Fatalf("committed output mismatch: %q", out)
	}
	if got := budget.Snapshot().InputBytes; got != 30 {
		t.Fatalf("new authorized request must reset budget, got %d", got)
	}

	// 返回的是拷贝：篡改调用方切片不影响后续提交。
	out[0] = 'Z'
	again, err := goodProc.Process(context.Background(), Fragment{RequestID: "req-3", Payload: []byte("good")})
	if err != nil || again[0] != 'A' {
		t.Fatalf("committed output must be an isolated copy, got %q err=%v", again, err)
	}
}

// TestSeparateBudgetsTrackedIndependently 三类成本各自累计、独立触发上限。
func TestSeparateBudgetsTrackedIndependently(t *testing.T) {
	cases := []struct {
		name     string
		limits   Limits
		category string
	}{
		{"input cap", Limits{MaxInputBytes: 25, MaxOutputBytes: 1000, MaxDictOps: 100}, CostInput},
		{"output cap", Limits{MaxInputBytes: 1000, MaxOutputBytes: 45, MaxDictOps: 100}, CostOutput},
		{"dict cap", Limits{MaxInputBytes: 1000, MaxOutputBytes: 1000, MaxDictOps: 5}, CostDict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factory := &scriptedFactory{steps: failAtLastStep()}
			verifier := &rejectingVerifier{}
			proc := NewProcessor(NewBudget("r", tc.limits), factory, verifier)

			_, err := proc.Process(context.Background(), Fragment{RequestID: "r", Payload: []byte("x")})
			var budgetErr *BudgetExceededError
			if !errors.As(err, &budgetErr) {
				t.Fatalf("want budget error, got %v", err)
			}
			if budgetErr.Category != tc.category {
				t.Fatalf("want %s cap, got %s", tc.category, budgetErr.Category)
			}
		})
	}
}

// TestBudgetChargeAtomicReject 超限时整笔记账拒绝，已累计值保持不变。
func TestBudgetChargeAtomicReject(t *testing.T) {
	b := NewBudget("r", Limits{MaxInputBytes: 10, MaxOutputBytes: 10, MaxDictOps: 10})
	if err := b.Charge(Cost{InputBytes: 5, OutputBytes: 5, DictOps: 5}, 1); err != nil {
		t.Fatal(err)
	}
	err := b.Charge(Cost{InputBytes: 1, OutputBytes: 99, DictOps: 1}, 1)
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) || budgetErr.Category != CostOutput {
		t.Fatalf("want output budget error, got %v", err)
	}
	got := b.Snapshot()
	if got.InputBytes != 5 || got.OutputBytes != 5 || got.DictOps != 5 {
		t.Fatalf("rejected charge must roll back fully, got %+v", got)
	}
}

// TestResetOnlyOnNewAuthorization 相同 RequestID 的 Reset/Bind 不刷新预算。
func TestResetOnlyOnNewAuthorization(t *testing.T) {
	b := NewBudget("r", Limits{MaxInputBytes: 100, MaxOutputBytes: 100, MaxDictOps: 100})
	if err := b.Charge(Cost{InputBytes: 40}, 1); err != nil {
		t.Fatal(err)
	}
	b.Reset("r", Limits{MaxInputBytes: 1}) // 同 ID：忽略
	if got := b.Snapshot().InputBytes; got != 40 {
		t.Fatalf("same-ID reset must not clear usage, got %d", got)
	}
	b.Reset("other", Limits{MaxInputBytes: 1}) // 新授权：清零
	if got := b.Snapshot().InputBytes; got != 0 {
		t.Fatalf("new-ID reset must clear usage, got %d", got)
	}

	attempt, reset := b.BindRequest("other")
	if reset || attempt != 1 {
		t.Fatalf("retry of same ID: reset=%v attempt=%d", reset, attempt)
	}
	attempt, reset = b.BindRequest("other")
	if reset || attempt != 2 {
		t.Fatalf("second retry: reset=%v attempt=%d", reset, attempt)
	}
	attempt, reset = b.BindRequest("brand-new")
	if !reset || attempt != 1 {
		t.Fatalf("new ID: reset=%v attempt=%d", reset, attempt)
	}
}

// TestContextCanceledBeforeStep 上下文取消时不产生输出也不驱动解压器。
func TestContextCanceledBeforeStep(t *testing.T) {
	factory := &scriptedFactory{steps: failAtLastStep()}
	verifier := &rejectingVerifier{}
	proc := NewProcessor(
		NewBudget("r", Limits{MaxInputBytes: 1000, MaxOutputBytes: 1000, MaxDictOps: 100}),
		factory, verifier,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := proc.Process(ctx, Fragment{RequestID: "r", Payload: []byte("x")})
	if !errors.Is(err, context.Canceled) || out != nil {
		t.Fatalf("want context.Canceled with nil output, got %v %q", err, out)
	}
	if got := atomic.LoadInt64(&factory.totalNext); got != 0 {
		t.Fatalf("canceled context must not drive decompressor, Next=%d", got)
	}
}

// TestCostIndependentOfNetworkBytes 记账依据实际解压工作量，与载荷大小无关：
// 极小的重传载荷同样按解压器自报成本累计，无法借“已接收字节未增长”逃避预算。
func TestCostIndependentOfNetworkBytes(t *testing.T) {
	factory := &scriptedFactory{steps: failAtLastStep()}
	verifier := &rejectingVerifier{}
	budget := NewBudget("r", Limits{MaxInputBytes: 30, MaxOutputBytes: 1000, MaxDictOps: 100})
	proc := NewProcessor(budget, factory, verifier)

	// 载荷只有 4 字节，但每步自报消耗 10 输入字节：一次尝试后输入预算恰好用尽。
	_, err := proc.Process(context.Background(), Fragment{RequestID: "r", Payload: []byte("tiny")})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("want verify failure, got %v", err)
	}
	_, err = proc.Process(context.Background(), Fragment{RequestID: "r", Payload: []byte("tiny")})
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) || budgetErr.Category != CostInput {
		t.Fatalf("replaying tiny payload must hit input budget, got %v", err)
	}
}
