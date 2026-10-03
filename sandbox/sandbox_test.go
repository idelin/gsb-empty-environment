package sandbox_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"rulesandbox/sandbox"
)

func newSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	sb := sandbox.New()
	err := sb.AddTenant(&sandbox.Tenant{
		ID: "acme",
		Limits: sandbox.Limits{
			MaxSteps:  1000,
			MaxAllocs: 100000,
			Timeout:   2 * time.Second,
		},
		Functions: sandbox.DefaultFunctions(),
		Variables: []string{"total", "vip", "coupon", "base", "greeting"},
		Rules: map[string]string{
			"discount": `total > 100 && (vip || coupon == "YES")`,
			"score":    `max(0, min(100, base * 1.5 + 10))`,
			"loop":     `eval(self)`,
			"concat":   `greeting + "-" + greeting`,
		},
	})
	if err != nil {
		t.Fatalf("AddTenant: %v", err)
	}
	return sb
}

func TestArithmeticAndPrecedence(t *testing.T) {
	sb := newSandbox(t)
	v, err := sb.RunExpr("acme", `1 + 2 * 3 - (4 / 2) % 3`, nil)
	if err != nil {
		t.Fatalf("RunExpr: %v", err)
	}
	if v != 5.0 {
		t.Fatalf("got %v, want 5", v)
	}
}

func TestBooleanRuleWithVars(t *testing.T) {
	sb := newSandbox(t)
	v, err := sb.Run("acme", "discount", map[string]any{
		"total":  150.0,
		"vip":    false,
		"coupon": "YES",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v != true {
		t.Fatalf("got %v, want true", v)
	}
}

func TestWhitelistedFunctions(t *testing.T) {
	sb := newSandbox(t)
	v, err := sb.Run("acme", "score", map[string]any{"base": 40.0})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v != 70.0 {
		t.Fatalf("got %v, want 70", v)
	}
}

func TestStringConcat(t *testing.T) {
	sb := newSandbox(t)
	v, err := sb.Run("acme", "concat", map[string]any{"greeting": "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v != "hi-hi" {
		t.Fatalf("got %v, want hi-hi", v)
	}
}

// Acceptance criterion: the malicious self-recursive formula eval(self)
// must be force-interrupted at the 1000-step budget with BudgetExceeded,
// without deadlocking or exhausting host memory.
func TestEvalSelfBudgetExceeded(t *testing.T) {
	sb := newSandbox(t)
	start := time.Now()
	_, err := sb.Run("acme", "loop", nil)
	if !errors.Is(err, sandbox.ErrBudgetExceeded) {
		t.Fatalf("got %v, want BudgetExceeded", err)
	}
	if !strings.Contains(err.Error(), "BudgetExceeded") {
		t.Fatalf("error message %q does not contain BudgetExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("interruption took too long: %v", elapsed)
	}
	// Host is still healthy: the sandbox keeps serving afterwards.
	for i := 0; i < 3; i++ {
		if _, err := sb.Run("acme", "loop", nil); !errors.Is(err, sandbox.ErrBudgetExceeded) {
			t.Fatalf("run %d: got %v, want BudgetExceeded", i, err)
		}
	}
	v, err := sb.Run("acme", "discount", map[string]any{"total": 200.0, "vip": true})
	if err != nil || v != true {
		t.Fatalf("sandbox unusable after budget kill: v=%v err=%v", v, err)
	}
}

func TestUnauthorizedFunctionRejected(t *testing.T) {
	sb := newSandbox(t)
	for _, expr := range []string{
		`system("rm -rf /")`,
		`exec("id")`,
		`getenv("HOME")`,
		`1 + shell("whoami")`,
	} {
		_, err := sb.RunExpr("acme", expr, nil)
		if !errors.Is(err, sandbox.ErrUnauthorizedFunction) {
			t.Fatalf("expr %q: got %v, want UnauthorizedFunction", expr, err)
		}
	}
}

func TestMemoryBudgetExceeded(t *testing.T) {
	sb := sandbox.New()
	err := sb.AddTenant(&sandbox.Tenant{
		ID:        "tiny-mem",
		Limits:    sandbox.Limits{MaxSteps: 100000, MaxAllocs: 10, Timeout: time.Second},
		Variables: []string{"s"},
		Rules:     map[string]string{"grow": `s + s + s + s + s + s + s + s`},
	})
	if err != nil {
		t.Fatalf("AddTenant: %v", err)
	}
	_, err = sb.Run("tiny-mem", "grow", map[string]any{"s": "0123456789"})
	if !errors.Is(err, sandbox.ErrMemoryExceeded) {
		t.Fatalf("got %v, want MemoryExceeded", err)
	}
}

func TestTimeoutInterrupts(t *testing.T) {
	sb := sandbox.New()
	err := sb.AddTenant(&sandbox.Tenant{
		ID:     "slow",
		Limits: sandbox.Limits{MaxSteps: 1000000, MaxAllocs: 1000000, Timeout: 50 * time.Millisecond},
		Functions: map[string]sandbox.Func{
			"sleep": func(args []any) (any, error) {
				time.Sleep(5 * time.Second)
				return 0.0, nil
			},
		},
		Rules: map[string]string{"hang": `sleep(1)`},
	})
	if err != nil {
		t.Fatalf("AddTenant: %v", err)
	}
	start := time.Now()
	_, err = sb.Run("slow", "hang", nil)
	if !errors.Is(err, sandbox.ErrTimeout) {
		t.Fatalf("got %v, want Timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout not enforced promptly: %v", elapsed)
	}
}

func TestMultiTenantIsolation(t *testing.T) {
	sb := newSandbox(t)
	err := sb.AddTenant(&sandbox.Tenant{
		ID:     "globex", // no functions whitelisted at all
		Limits: sandbox.Limits{MaxSteps: 100, Timeout: time.Second},
		Rules:  map[string]string{"x": `max(1, 2)`},
	})
	if err != nil {
		t.Fatalf("AddTenant: %v", err)
	}
	if _, err := sb.Run("globex", "x", nil); !errors.Is(err, sandbox.ErrUnauthorizedFunction) {
		t.Fatalf("tenant without whitelist: got %v, want UnauthorizedFunction", err)
	}
	if _, err := sb.Run("nope", "x", nil); err == nil {
		t.Fatal("unknown tenant should fail")
	}
}

func TestStaticValidation(t *testing.T) {
	sb := newSandbox(t)
	if _, err := sb.RunExpr("acme", `unknown_var + 1`, nil); !errors.Is(err, sandbox.ErrValidation) {
		t.Fatalf("unknown variable: got %v, want ValidationError", err)
	}
	if _, err := sb.RunExpr("acme", `1 + * 2`, nil); err == nil {
		t.Fatal("syntax error should fail")
	}
	deep := strings.Repeat("-", 500) + "1" // 500 nested unary nodes
	if _, err := sb.RunExpr("acme", deep, nil); !errors.Is(err, sandbox.ErrValidation) {
		t.Fatalf("deep nesting: got %v, want ValidationError", err)
	}
	absurd := strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000)
	if _, err := sb.RunExpr("acme", absurd, nil); err == nil {
		t.Fatal("pathological parenthesization should be rejected at parse time")
	}
}

func TestRuntimeErrors(t *testing.T) {
	sb := newSandbox(t)
	if _, err := sb.RunExpr("acme", `1 / 0`, nil); err == nil {
		t.Fatal("division by zero should fail")
	}
	if _, err := sb.RunExpr("acme", `eval("missing_rule")`, nil); err == nil {
		t.Fatal("eval of unknown rule should fail")
	}
	if _, err := sb.RunExpr("acme", `1 && true`, nil); err == nil {
		t.Fatal("type error should fail")
	}
}
