# linqueue — 小型有界队列的线性化检查器

给定一个有界 FIFO 队列的并发调用历史（调用/返回时刻、返回值、异常、
未完成调用），穷举搜索满足**实时顺序（real-time order）**的串行解释
（linearization）；找不到时给出**最小反例**，并对反例做**可重放**验证。

## 判定规则

- **实时偏序**：若调用 A 的返回时刻严格早于调用 B 的发起时刻，
  则 A 必须排在 B 之前；重叠的调用可以任意重排
  （“后发先返”的操作允许换序）。
- **未完成调用（pending）**：按 Herlihy-Wing 语义，可丢弃
  （当作未生效），也可放在满足实时约束的任意位置生效；
  所有已完成调用必须出现在解释中。
- **异常是一等结果**：空队 `deq` 只允许返回 `EMPTY`，
  满队 `enq` 只允许返回 `FULL`；返回结果必须与模型转换一致。
- **成功出队值必须来自合法入队**：`deq -> ok(X)` 要求该位置模型
  队首正好是 `X`，不允许“凭空出队”。
- **不按完成时间排序**：检查器对满足实时偏序的拓扑序做 DFS+记忆化搜索，
  而不是简单按 response 时间排序（见示例 1 与对应测试）。
- **搜索不完则保持未决**：状态数超过 `budget` 时结论为 `unknown`，
  绝不会在未证伪时给出反例。

## 结构

- `linqueue/model.py` — 有限状态模型：容量有界的 FIFO 队列，
  状态为元组；`enq`/`deq` 的每一步给出合法的 `(新状态, 返回)`。
- `linqueue/history.py` — 调用历史 `Op`（id、方法、参数、结果、
  invoke/response 时刻；`response is None` 表示未完成）。
- `linqueue/checker.py` — 穷举搜索，返回 `linearizable`
  （附具体 witness 顺序）/ `not-linearizable` / `unknown`。
- `linqueue/counterexample.py` — 贪心求 1-最小反例；
  枚举反例的全部候选串行解释逐条重放（pending 子集 × 拓扑序），
  记录每个候选第一处被模型拒绝的步骤；witness 也可重放校验。

## 运行

需要 Python 3.11，仅使用标准库。

```bash
/opt/homebrew/bin/python3.11 -m linqueue                    # 运行样例
/opt/homebrew/bin/python3.11 -m unittest discover -s tests  # 运行测试
```

## 用法片段

```python
from linqueue import (QueueModel, Ret, enq, deq, pending_enq,
                      check, Verdict, minimize,
                      replay_counterexample, replay_witness)

model = QueueModel(capacity=2)
history = [
    enq(1, "A", invoke=0, response=10),                 # 慢调用
    enq(2, "B", invoke=1, response=2),                  # 后发先返
    deq(3, invoke=3, response=4, result=Ret("ok", "A")),
]
result = check(model, history, budget=100_000)
assert result.verdict is Verdict.LINEARIZABLE
replay_witness(model, result.linearization)   # 在模型上逐步重放 witness

bad = [enq(1, "A", 0, 1), deq(2, 2, 3, result=Ret("ok", "B"))]
assert check(model, bad).verdict is Verdict.NOT_LINEARIZABLE
core = minimize(model, bad)                   # 最小反例子历史
report = replay_counterexample(model, core)   # 每个候选解释都失败
print(report.failures[0].describe())
```

## 测试结果

`/opt/homebrew/bin/python3.11 -m unittest discover -s tests -v`：
**21 个测试全部通过**（`Ran 21 tests ... OK`），覆盖：

- 纯顺序历史、重叠调用重排、完成时间排序错误反例；
- 合法/非法 `EMPTY`、合法/非法 `FULL`；
- pending 调用可丢弃、必须生效、复杂交织与 pending deq；
- 出队值无合法来源、出队早于入队；
- 最小反例提取（剔除无关调用）与 1-最小性；
- 预算耗尽保持 `unknown`，且 `unknown` 不会产出反例；
- 反例全部候选解释重放失败、伪造反例被重放驳回、witness 重放校验。
