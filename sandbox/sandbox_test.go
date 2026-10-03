package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidExpressions(t *testing.T) {
	sb := New(Config{})
	cases := []struct {
		expr string
		env  map[string]any
		want any
	}{
		{"1 + 2 * 3", nil, float64(7)},
		{"(1 + 2) * 3", nil, float64(9)},
		{"10 / 4", nil, float64(2.5)},
		{"10 % 3", nil, float64(1)},
		{"-5 + abs(-3)", nil, float64(-2)},
		{"score >= 60 && bonus > 0", map[string]any{"score": 80.0, "bonus": 5.0}, true},
		{"score >= 60 && bonus > 0", map[string]any{"score": 40.0, "bonus": 5.0}, false},
		{"!(a == b) || a < b", map[string]any{"a": 1.0, "b": 2.0}, true},
		{"max(a, b, 7) * 2", map[string]any{"a": 3.0, "b": 9.0}, float64(18)},
		{"min(4, 2, 8)", nil, float64(2)},
		{"floor(3.7) + ceil(0.1) + round(2.5)", nil, float64(7)},
		{`name == "alice" && role != "guest"`, map[string]any{"name": "alice", "role": "admin"}, true},
		{`"hello" + " " + "world"`, nil, "hello world"},
		{`len("abcd")`, nil, float64(4)},
		{`concat("a", "b", "c")`, nil, "abc"},
		{`len(concat("ab", "cd")) == 4`, nil, true},
		{`eval("1 + 2")`, nil, float64(3)},
		{`eval(expr)`, map[string]any{"expr": "max(1, 5) * 2"}, float64(10)},
		{"true && false || true", nil, true},
	}
	for _, c := range cases {
		got, err := sb.Run(context.Background(), c.expr, c.env)
		if err != nil {
			t.Errorf("Run(%q) error: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("Run(%q) = %v (%T), want %v (%T)", c.expr, got, got, c.want, c.want)
		}
	}
}

// TestEvalSelfBudgetExceeded 验收用例:恶意无限递归公式 eval(self)
// 必须在 1000 步预算处被强制中断,返回 BudgetExceeded,宿主无死锁。
func TestEvalSelfBudgetExceeded(t *testing.T) {
	sb := New(Config{MaxSteps: 1000})
	env := map[string]any{"self": "eval(self)"}

	start := time.Now()
	_, err := sb.Run(context.Background(), "eval(self)", env)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("interruption took too long (%v), possible deadlock", elapsed)
	}
	t.Logf("eval(self) interrupted after %v with: %v", elapsed, err)
}

// TestEvalSelfConcurrent 多租户并发执行恶意公式,验证无共享状态死锁。
func TestEvalSelfConcurrent(t *testing.T) {
	sb := New(Config{MaxSteps: 1000})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := sb.Run(context.Background(), "eval(self)", map[string]any{"self": "eval(self)"})
			if !errors.Is(err, ErrBudgetExceeded) {
				t.Errorf("expected ErrBudgetExceeded, got %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestMemoryBudgetExceeded(t *testing.T) {
	// eval 递归每层帧计费 256 字节,小内存配额下先于步数预算耗尽。
	sb := New(Config{MaxSteps: 100000, MaxMemoryBytes: 2048, MaxEvalDepth: 100000})
	_, err := sb.Run(context.Background(), "eval(self)", map[string]any{"self": "eval(self)"})
	if !errors.Is(err, ErrMemoryExceeded) {
		t.Fatalf("expected ErrMemoryExceeded, got %v", err)
	}
}

func TestStringAllocMemoryBudget(t *testing.T) {
	sb := New(Config{MaxMemoryBytes: 4096})
	big := strings.Repeat("x", 1000)
	_, err := sb.Run(context.Background(), "concat(s, s, s, s, s)", map[string]any{"s": big})
	if !errors.Is(err, ErrMemoryExceeded) {
		t.Fatalf("expected ErrMemoryExceeded, got %v", err)
	}
}

func TestTimeoutInterrupt(t *testing.T) {
	sb := New(Config{
		MaxSteps:       1 << 40,
		MaxMemoryBytes: 1 << 40,
		MaxEvalDepth:   1 << 20,
		Timeout:        50 * time.Millisecond,
	})
	start := time.Now()
	_, err := sb.Run(context.Background(), "eval(self)", map[string]any{"self": "eval(self)"})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout interruption too slow: %v", elapsed)
	}
}

func TestContextCancellation(t *testing.T) {
	sb := New(Config{MaxSteps: 1 << 40, MaxMemoryBytes: 1 << 40, MaxEvalDepth: 1 << 20, Timeout: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := sb.Run(ctx, "eval(self)", map[string]any{"self": "eval(self)"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout on cancel, got %v", err)
	}
}

func TestUnauthorizedFunctionRejected(t *testing.T) {
	sb := New(Config{})
	for _, expr := range []string{
		`system("rm -rf /")`,
		`exec("ls")`,
		`readFile("/etc/passwd")`,
		`foo(1)`,
	} {
		_, err := sb.Run(context.Background(), expr, nil)
		if !errors.Is(err, ErrValidation) {
			t.Errorf("Run(%q): expected ErrValidation, got %v", expr, err)
		}
	}
}

func TestCustomWhitelist(t *testing.T) {
	sb := New(Config{AllowedFuncs: map[string]bool{"max": true}})
	if _, err := sb.Run(context.Background(), "max(1, 2)", nil); err != nil {
		t.Fatalf("max should be allowed: %v", err)
	}
	if _, err := sb.Run(context.Background(), "abs(1)", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("abs should be rejected by custom whitelist")
	}
	if _, err := sb.Run(context.Background(), `eval("1")`, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("eval should be rejected by custom whitelist")
	}
}

func TestEvalErrors(t *testing.T) {
	sb := New(Config{})
	cases := []struct {
		expr string
		env  map[string]any
	}{
		{"1 / 0", nil},
		{"1 % 0", nil},
		{"undefined_var + 1", nil},
		{`-"abc"`, nil},
		{`!1`, nil},
		{`1 && true`, nil},
		{`"a" - "b"`, nil},
		{`abs("x")`, nil},
		{`len(1)`, nil},
	}
	for _, c := range cases {
		_, err := sb.Run(context.Background(), c.expr, c.env)
		if !errors.Is(err, ErrEval) {
			t.Errorf("Run(%q): expected ErrEval, got %v", c.expr, err)
		}
	}
}

func TestValidationLimits(t *testing.T) {
	sb := New(Config{})
	// 表达式过长
	if _, err := sb.Run(context.Background(), strings.Repeat("1+", 3000)+"1", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for oversized expression, got %v", err)
	}
	// 嵌套过深(解析器保护宿主栈)
	deep := strings.Repeat("(", 500) + "1" + strings.Repeat(")", 500)
	if _, err := New(Config{MaxSteps: 1 << 20}).Run(context.Background(), deep, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for deep nesting, got %v", err)
	}
	// 语法错误
	if _, err := sb.Run(context.Background(), "1 +", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for syntax error, got %v", err)
	}
}

func TestNodeAndDepthLimits(t *testing.T) {
	sb := New(Config{MaxNodes: 10})
	if _, err := sb.Run(context.Background(), "1+2+3+4+5+6+7+8+9+10+11+12", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for node limit, got %v", err)
	}
	sb2 := New(Config{MaxASTDepth: 5})
	expr := strings.Repeat("-", 20) + "1"
	if _, err := sb2.Run(context.Background(), expr, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for AST depth limit, got %v", err)
	}
}

func TestSmallStepBudget(t *testing.T) {
	sb := New(Config{MaxSteps: 5})
	if _, err := sb.Run(context.Background(), "1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 10", nil); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}
