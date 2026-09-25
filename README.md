# linreg — 单键寄存器线性一致性检查器与事件模拟器

纯 Go 标准库实现，无第三方依赖。两部分：

- **检查器**（`checker.go`）：对带调用/返回区间的单键寄存器历史做线性一致性判定，
  回溯搜索所有满足实时序的顺序，输出**见证**（合法顺序）或**反例证据**（穷举失败统计 /
  直接矛盾），搜索预算耗尽时输出 **UNKNOWN**。
- **模拟器**（`simulator.go`）：单进程事件模拟器，通信（消息延迟/丢弃）、故障（副本崩溃）
  与时间（虚拟时钟）全部由场景显式控制；内含正确的 ABD 风格仲裁读写协议和一个
  有 bug 的单副本读实现，用于产生安全性与活性证据。

## 语义约定

- **实时序**：操作 a 的返回时间严格小于 b 的调用时间，则 a 必须先于 b（`history.go: precedes`）。
- **重叠写**：并发区间内的写可按任意顺序线性化。
- **未完成调用**（Wing-Herlihy 扩展语义）：
  - 未完成**写**：可被"补全"（放置在其实时前驱之后任意位置）或"移除"——两者都在搜索中尝试；
    它可能是某个已完成读返回值的唯一依据。
  - 未完成**读**：返回值未知，视为通配；且没有任何操作被排在其后，因此总能附加在
    线性化末尾，不影响判定（见证中仍列出）。
- **预算**：`Checker.Budget` 限制搜索节点数，耗尽即 UNKNOWN，绝不猜测结论。
- 搜索用 `(已放置集合, 已解决集合, 当前寄存器值)` 做备忘录剪枝。

## 运行

```sh
go run .                  # 全部场景
go run . -scenario bad    # 单个场景: good|crash|bad|pending|unknown
go test ./...             # 测试
```

场景说明：

- `good`：无故障仲裁协议 → 活性证据（5/5 调用完成）+ 安全性证据（LINEARIZABLE + 见证）。
- `crash`：3 副本中崩溃 1 个，仲裁仍可用 → 活性与安全性均保持。
- `bad`：有 bug 的单副本读 + 被滞留的 STORE 消息 → NOT LINEARIZABLE + 反例证据。
- `pending`：手工构造的含未完成调用历史 → 扩展语义判定。
- `unknown`：9 个互相重叠的写 + 紧预算 → UNKNOWN；全预算 → LINEARIZABLE + 见证。

## 运行样例（`go run .` 节选）

```
=== scenario "bad" (simulator) ===
liveness evidence: 2/2 invocations completed by virtual time 52
history:
  #1 write(1)  [0,2]
  #2 read()->0   [10,12]
safety evidence: verdict: NOT LINEARIZABLE (2 search nodes, 2 memoized states)
evidence: exhaustive search over all real-time-respecting orders (2 nodes, 2 distinct states): every order violates the register semantics

=== scenario "unknown" (hand-built history, tight budget) ===
-- budget 200 --
verdict: UNKNOWN (232 search nodes, 93 memoized states)
evidence: budget exhausted after 232 search nodes (93 distinct failure states); no verdict
-- full budget --
verdict: LINEARIZABLE (3639 search nodes, 1032 memoized states)
witness: W#2(2)  W#3(3)  W#4(4)  W#5(5)  W#6(6)  W#7(7)  W#8(8)  W#9(9)  W#1(1)  R#10->1
```

## 测试结果（`go test -v ./...`）

13 个测试全部通过，覆盖：实时序强制、重叠写任意顺序、未写过的值、
未完成写补全/移除、未完成读通配、初始值、预算耗尽 UNKNOWN、非法输入、
正确协议的安全性与活性、崩溃容错、bug 协议反例、丢包破坏活性。

```
--- PASS: TestRealTimeOrderEnforced
--- PASS: TestOverlappingWritesAnyOrder
--- PASS: TestNeverWrittenValue
--- PASS: TestPendingWriteJustifiesRead
--- PASS: TestPendingWriteCanBeDropped
--- PASS: TestPendingReadIsWildcard
--- PASS: TestInitialValue
--- PASS: TestBudgetExhausted
--- PASS: TestInvalidHistory
--- PASS: TestCorrectProtocolSafetyAndLiveness
--- PASS: TestCorrectProtocolSurvivesCrash
--- PASS: TestBuggyProtocolCounterexample
--- PASS: TestDropsBreakLiveness
PASS
ok  	linreg
```
