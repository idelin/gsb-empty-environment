// 运行样例:多租户规则沙箱演示。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rulesandbox/sandbox"
)

func run(sb *sandbox.Sandbox, label, expr string, env map[string]any) {
	start := time.Now()
	v, err := sb.Run(context.Background(), expr, env)
	elapsed := time.Since(start)
	if err != nil {
		kind := "error"
		switch {
		case errors.Is(err, sandbox.ErrBudgetExceeded):
			kind = "BudgetExceeded"
		case errors.Is(err, sandbox.ErrMemoryExceeded):
			kind = "MemoryExceeded"
		case errors.Is(err, sandbox.ErrTimeout):
			kind = "Timeout"
		case errors.Is(err, sandbox.ErrValidation):
			kind = "Validation"
		}
		fmt.Printf("[%-12s] %-40s => %-14s (%v)  [%v]\n", label, expr, kind, err, elapsed.Round(time.Microsecond))
		return
	}
	fmt.Printf("[%-12s] %-40s => %v  [%v]\n", label, expr, v, elapsed.Round(time.Microsecond))
}

func main() {
	sb := sandbox.New(sandbox.Config{MaxSteps: 1000})

	fmt.Println("== 正常规则表达式 ==")
	run(sb, "tenant-A", "score >= 60 && bonus > 0", map[string]any{"score": 82.0, "bonus": 3.0})
	run(sb, "tenant-A", "max(price * 0.8, 100)", map[string]any{"price": 200.0})
	run(sb, "tenant-B", `level == "vip" || spend > 10000`, map[string]any{"level": "normal", "spend": 12000.0})
	run(sb, "tenant-B", `len(name) >= 3 && !blocked`, map[string]any{"name": "alice", "blocked": false})

	fmt.Println("\n== 未授权函数拦截 ==")
	run(sb, "malicious", `system("rm -rf /")`, nil)
	run(sb, "malicious", `readFile("/etc/passwd")`, nil)

	fmt.Println("\n== 恶意无限递归公式 eval(self) ==")
	run(sb, "malicious", "eval(self)", map[string]any{"self": "eval(self)"})

	fmt.Println("\n== 内存炸弹(小内存配额) ==")
	tight := sandbox.New(sandbox.Config{MaxSteps: 100000, MaxMemoryBytes: 2048, MaxEvalDepth: 100000})
	run(tight, "malicious", "eval(self)", map[string]any{"self": "eval(self)"})

	fmt.Println("\n== 死循环超时(大预算 + 50ms 超时) ==")
	slow := sandbox.New(sandbox.Config{MaxSteps: 1 << 40, MaxMemoryBytes: 1 << 40, MaxEvalDepth: 1 << 20, Timeout: 50 * time.Millisecond})
	run(slow, "malicious", "eval(self)", map[string]any{"self": "eval(self)"})

	fmt.Println("\n宿主进程存活,无死锁、无内存溢出。")
}
