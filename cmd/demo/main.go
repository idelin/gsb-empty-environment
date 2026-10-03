// Demo: multi-tenant rule sandbox with execution budget guards.
package main

import (
	"errors"
	"fmt"
	"time"

	"rulesandbox/sandbox"
)

func main() {
	sb := sandbox.New()

	err := sb.AddTenant(&sandbox.Tenant{
		ID: "acme",
		Limits: sandbox.Limits{
			MaxSteps:  1000, // instruction-step budget per execution
			MaxAllocs: 100000,
			Timeout:   2 * time.Second,
		},
		Functions: sandbox.DefaultFunctions(), // whitelist: min/max/abs/floor/ceil/round
		Variables: []string{"total", "vip", "coupon", "base"},
		Rules: map[string]string{
			"discount_ok": `total > 100 && (vip || coupon == "YES")`,
			"risk_score":  `max(0, min(100, base * 1.5 + 10))`,
			"loop":        `eval(self)`, // malicious: infinite recursion
		},
	})
	must(err)

	fmt.Println("== 1. normal boolean rule ==")
	print(sb.Run("acme", "discount_ok", map[string]any{
		"total": 150.0, "vip": false, "coupon": "YES",
	}))

	fmt.Println("== 2. arithmetic rule with whitelisted functions ==")
	print(sb.Run("acme", "risk_score", map[string]any{"base": 40.0}))

	fmt.Println("== 3. unauthorized host function call (rejected at AST validation) ==")
	print(sb.RunExpr("acme", `system("rm -rf /")`, nil))

	fmt.Println("== 4. malicious infinite recursion: eval(self) ==")
	start := time.Now()
	v, err := sb.Run("acme", "loop", nil)
	fmt.Printf("result=%v err=%v (interrupted after %v)\n", v, err, time.Since(start).Round(time.Millisecond))
	if !errors.Is(err, sandbox.ErrBudgetExceeded) {
		panic("expected BudgetExceeded")
	}

	fmt.Println("== 5. host still healthy after budget kill ==")
	print(sb.Run("acme", "discount_ok", map[string]any{"total": 200.0, "vip": true}))
}

func print(v any, err error) {
	fmt.Printf("result=%v err=%v\n", v, err)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
