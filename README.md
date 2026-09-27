# 整数时钟时间自动机可达性检查器

纯 Python 3.11 标准库实现（测试用 `unittest`）。对给定的有限状态时间自动机做可达性检查，输出可重放的时间推进轨迹。

## 模型

- **状态机**：位置（location）+ 迁移（transition）；迁移携带 guard（时钟与整数常数的比较合取：`< <= > >= ==`）与 reset（时钟归零集合）。
- **整数时钟**：时钟取整数值，初始为 0，reset 后置 0（旧值不保留），时间以整数延迟推进。
- **时间上界 T**：全局 elapsed time 不超过 T。

## 显式截断 / 等价规则（保证有限性）

1. **整数化**：初值、reset 值、guard 常数均为整数，故只需考虑整数延迟；任何分数延迟可达的状态整数延迟亦可达。
2. **时间上界**：全局时间 ≤ T，只有有限个整数时刻。
3. **截断等价**：时钟值超过 `CAP = max(T, 最大 guard 常数)` 时截断为 CAP。所有 guard 常数严格小于 CAP，故 guard 对真实值与截断值的求值完全一致——截断状态构成有限等价商。
4. **零时间环终止**：零时间后继在有限 visited 集合上封闭，零时间环必然重访已见状态，不会无限扩展。

## 判定

| 判定 | 含义 |
|---|---|
| `REACHABLE` | 找到 witness 轨迹，可逐步重放验证（`replay()`） |
| `UNREACHABLE` | 整个截断状态空间已搜索完，目标不可达（结论性） |
| `UNKNOWN` | 状态预算耗尽、搜索未完成，保持未决，不下结论 |

反例/见证可重放：`replay(model, trace)` 重新执行每一步，校验延迟合法性、guard 满足、记录的中间状态一致，任何篡改都会抛 `ReplayError`。

## 运行

```bash
python3.11 timed_automaton.py      # 演示样例
python3.11 -m unittest test_timed_automaton -v   # 测试
```

## 运行样例

演示模型：`idle --start,{x}:=0--> run`，`run --finish[x>=2 & x<4]--> done`，`run --timeout[x>=4]--> failed`，时间上界 10。

```
=== demo model ===
clocks: ('x',), initial: idle, time bound: 10, truncation cap: 11
  idle --start--> run  start {x}:=0
  run --finish--> done  finish [x>=2 & x<4]
  run --timeout--> failed  timeout [x>=4]

--- target: done ---
verdict: REACHABLE (target 'done')
explored states: 11, pending frontier: 5
witness trace (replayable):
  start ==> (idle, {x=0})
  take start {x}:=0  ==>  (run, {x=0})
  delay 1  ==>  (run, {x=1})
  delay 1  ==>  (run, {x=2})
  take finish [x>=2 & x<4]  ==>  (done, {x=2})
replay check: OK (replayed final state (done, {x=2}))

--- target: failed ---
verdict: REACHABLE (target 'failed')
witness trace (replayable):
  start ==> (idle, {x=0})
  take start {x}:=0  ==>  (run, {x=0})
  delay 1  ==>  (run, {x=1})
  delay 1  ==>  (run, {x=2})
  delay 1  ==>  (run, {x=3})
  delay 1  ==>  (run, {x=4})
  take timeout [x>=4]  ==>  (failed, {x=4})
replay check: OK (replayed final state (failed, {x=4}))

--- target: nonexistent ---
verdict: UNREACHABLE (target 'nonexistent')
explored states: 150, pending frontier: 0
```

## 测试结果

`python3.11 -m unittest test_timed_automaton` → **14 tests, OK**，覆盖：

- **严格性**：`x<3` 与 `x<=3` 在边界 x=3 处给出不同判定（UNREACHABLE vs REACHABLE）
- **reset 语义**：reset 后旧时钟值不保留（reset 后 `x<2` 立即可满足，`x>=5` 不可由旧值满足）
- **零时间环**：无 guard/reset 的自环与双位置环均终止，不无限扩展
- **判定完备性**：搜索完成得 `REACHABLE`/`UNREACHABLE`；预算耗尽得 `UNKNOWN`（未决）
- **时间上界**：超出上界才可到达的目标判 `UNREACHABLE`
- **可重放性**：witness 重放到目标；篡改延迟或违反 guard 的轨迹被 `ReplayError` 拒绝
- **截断等价**：超过 CAP 的值被截断，状态空间有限，大 guard 常数仍正确求值
- **轨迹内容**：witness 轨迹显式包含时间推进（delay）步骤

```
Ran 14 tests in 0.001s

OK
```
