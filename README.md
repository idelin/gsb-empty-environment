# rulesandbox — 多租户规则沙箱与执行限额守护器

纯 Go 标准库实现。租户提交自定义布尔/算术表达式（规则），沙箱先对表达式做
抽象语法树（AST）静态校验，再在严格的指令步数、内存申请量与超时预算下执行，
超限立即中断，宿主进程不受拖累。

## 安全模型

- **AST 静态校验**（`sandbox/validate.go`）
  - 函数调用白名单：仅允许租户注册的宿主函数与内置 `eval`，其余一律
    `UnauthorizedFunction`，在求值前拒绝；
  - 变量白名单：未声明的标识符直接拒绝；
  - 限制 AST 嵌套深度与节点总数；解析阶段另有递归深度上限，
    防止病态括号输入打爆宿主栈。
- **执行限额守护**（`sandbox/eval.go`）
  - 每求值一个 AST 节点计 1 条指令步，耗尽 `MaxSteps` 立即返回
    `BudgetExceeded`；
  - 每次中间值/字符串拼接按单位计入内存预算，耗尽 `MaxAllocs` 返回
    `MemoryExceeded`；
  - 每次执行带墙钟 `Timeout`，在 worker goroutine 中运行，超时即被
    select 放弃，宿主永不死锁。
- **多租户隔离**（`sandbox/sandbox.go`）：每个租户独立的限额、函数白名单、
  变量白名单与规则命名空间。
- `eval(name)` 是唯一可产生递归的内置特殊形式（`self` 绑定当前规则名），
  递归的每一步仍计入同一份预算，因此 `eval(self)` 无限递归会被确定性中断。

## 目录结构

- `sandbox/lex.go` — 词法分析
- `sandbox/parse.go` — 递归下降语法分析（带解析深度保护）
- `sandbox/validate.go` — AST 静态校验（白名单 / 深度 / 规模）
- `sandbox/eval.go` — 带步数、内存、超时预算的求值器
- `sandbox/sandbox.go` — 多租户注册与执行入口
- `sandbox/funcs.go` — 安全内置函数（min/max/abs/floor/ceil/round）
- `sandbox/sandbox_test.go` — go test 验收测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v     # 运行测试
go run ./cmd/demo    # 运行样例
```

## 样例输出（go run ./cmd/demo）

```
== 1. normal boolean rule ==
result=true err=<nil>
== 2. arithmetic rule with whitelisted functions ==
result=70 err=<nil>
== 3. unauthorized host function call (rejected at AST validation) ==
result=<nil> err=rule "__expr__": UnauthorizedFunction: call to non-whitelisted function: "system"
== 4. malicious infinite recursion: eval(self) ==
result=<nil> err=BudgetExceeded: instruction step budget exhausted (interrupted after 0s)
== 5. host still healthy after budget kill ==
result=true err=<nil>
```

## 测试结果（go test ./... -v）

```
=== RUN   TestArithmeticAndPrecedence
--- PASS: TestArithmeticAndPrecedence (0.00s)
=== RUN   TestBooleanRuleWithVars
--- PASS: TestBooleanRuleWithVars (0.00s)
=== RUN   TestWhitelistedFunctions
--- PASS: TestWhitelistedFunctions (0.00s)
=== RUN   TestStringConcat
--- PASS: TestStringConcat (0.00s)
=== RUN   TestEvalSelfBudgetExceeded
--- PASS: TestEvalSelfBudgetExceeded (0.00s)
=== RUN   TestUnauthorizedFunctionRejected
--- PASS: TestUnauthorizedFunctionRejected (0.00s)
=== RUN   TestMemoryBudgetExceeded
--- PASS: TestMemoryBudgetExceeded (0.00s)
=== RUN   TestTimeoutInterrupts
--- PASS: TestTimeoutInterrupts (0.05s)
=== RUN   TestMultiTenantIsolation
--- PASS: TestMultiTenantIsolation (0.00s)
=== RUN   TestStaticValidation
--- PASS: TestStaticValidation (0.00s)
=== RUN   TestRuntimeErrors
--- PASS: TestRuntimeErrors (0.00s)
PASS
ok  	rulesandbox/sandbox
```

验收边界：`TestEvalSelfBudgetExceeded` 执行恶意公式 `eval(self)`，沙箱在
1000 步指令预算处强制中断并返回 `BudgetExceeded`；随后连续多次执行同一恶意
规则均稳定返回同一错误，且其他规则照常求值——宿主进程无死锁、无内存溢出。
