# 多租户规则沙箱与执行限额守护器

用 Go 标准库实现的表达式安全求值沙箱。租户提交自定义布尔/算术表达式,
沙箱负责解析、AST 静态校验、限额执行,任何越界行为立即中断。

## 安全模型

- **AST 校验**:解析后静态遍历语法树,函数调用必须在白名单内
  (默认 `abs/floor/ceil/round/min/max/len/concat/eval`,全部为纯函数),
  严禁触达宿主环境的文件、网络、进程等未授权资源;
  同时限制表达式长度、AST 深度与节点数,解析器自带递归深度上限保护宿主栈。
- **指令步数预算**:解析、校验、求值、内建调用统一按步计费,
  超过 `MaxSteps`(默认 1000)立即返回 `ErrBudgetExceeded`。
- **内存配额**:字符串字面量、字符串拼接、`eval` 调用帧等内存申请计入
  `MaxMemoryBytes`,超额立即返回 `ErrMemoryExceeded`。
- **超时/取消**:支持 `Timeout` 与 `context.Context` 取消,触发返回 `ErrTimeout`。
- **沙箱内 `eval`**:允许表达式对字符串再求值(如 `eval(self)`),
  但与外层共享同一份步数/内存/超时预算,无限递归必然被预算中断,无法逃逸。

## 目录结构

```
sandbox/          沙箱核心(lexer/parser/validate/eval/funcs)
cmd/demo/         运行样例
```

## 运行

```sh
go test ./sandbox/ -v     # 测试(含验收用例)
go run ./cmd/demo         # 运行样例
```

## 验收用例

恶意无限递归公式 `eval(self)`(`env: self -> "eval(self)"`):
沙箱在 1000 步指令预算处强制中断,返回 `BudgetExceeded`,
实测中断耗时约 50µs,宿主进程无死锁、无内存溢出;
另有 32 路并发执行同一恶意公式的测试,`-race` 通过。

## 测试结果(go1.24.2, go test ./sandbox/ -v)

```
--- PASS: TestValidExpressions (0.00s)
--- PASS: TestEvalSelfBudgetExceeded (0.00s)   # eval(self) interrupted after ~55µs
--- PASS: TestEvalSelfConcurrent (0.00s)
--- PASS: TestMemoryBudgetExceeded (0.00s)
--- PASS: TestStringAllocMemoryBudget (0.00s)
--- PASS: TestTimeoutInterrupt (0.05s)
--- PASS: TestContextCancellation (0.03s)
--- PASS: TestUnauthorizedFunctionRejected (0.00s)
--- PASS: TestCustomWhitelist (0.00s)
--- PASS: TestEvalErrors (0.00s)
--- PASS: TestValidationLimits (0.00s)
--- PASS: TestNodeAndDepthLimits (0.00s)
--- PASS: TestSmallStepBudget (0.00s)
PASS
ok      rulesandbox/sandbox
```

## 运行样例输出(go run ./cmd/demo)

```
== 正常规则表达式 ==
[tenant-A    ] score >= 60 && bonus > 0                 => true
[tenant-A    ] max(price * 0.8, 100)                    => 160
[tenant-B    ] level == "vip" || spend > 10000          => true
[tenant-B    ] len(name) >= 3 && !blocked               => true

== 未授权函数拦截 ==
[malicious   ] system("rm -rf /")                       => Validation (unauthorized function "system")
[malicious   ] readFile("/etc/passwd")                  => Validation (unauthorized function "readFile")

== 恶意无限递归公式 eval(self) ==
[malicious   ] eval(self)                               => BudgetExceeded (step budget exceeded)

== 内存炸弹(小内存配额) ==
[malicious   ] eval(self)                               => MemoryExceeded (memory budget exceeded)

== 死循环超时(大预算 + 50ms 超时) ==
[malicious   ] eval(self)                               => Timeout (execution timeout)

宿主进程存活,无死锁、无内存溢出。
```

## 用法示例

```go
sb := sandbox.New(sandbox.Config{MaxSteps: 1000})
v, err := sb.Run(ctx, "score >= 60 && bonus > 0", map[string]any{
    "score": 82.0, "bonus": 3.0,
})
// v == true

// 自定义函数白名单(例如禁用 eval)
sb2 := sandbox.New(sandbox.Config{AllowedFuncs: map[string]bool{"max": true, "min": true}})
```
