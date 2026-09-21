# Revisable Token-Bucket Replay

限流规则复盘引擎：按事件时间回放请求日志，支持事后补录历史请求、
从历史任意时刻修订分段配置，并给出每次修订的**最小连续影响范围**，
范围之外的已稳定结果保持原身份（值与版本号都不变）。

## Files

- `token_bucket.py` — 增量可修订回放引擎（`ReplayEngine`）
- `oracle.py` — 独立实现的全量回放 oracle，与引擎不共享代码
- `revision_consumer.py` — 修订消费样例（`python3 revision_consumer.py`）
- `test_replay.py` — 测试（`python3 -m unittest -v`）

## Replay semantics

- 请求按 `(time, seq)` 排序；每个放行的请求消耗 1 个令牌。
- 桶按当前配置段的 `rate` 连续补充，封顶于该段 `capacity`。
- 配置段为半开区间 `[start, next_start)`；恰好落在边界上的请求适用
  **新**段。容量缩小时，桶在边界处先被钳制到新容量，再服务边界上
  同刻的请求。
- 桶初始为满（取首个请求时刻生效段的容量）。
- 每次判定输出：放行/拒绝原因（`token_available` /
  `insufficient_tokens`）、桶前后状态 `bucket_before/after`、结果版本
  `version`。

## Exact arithmetic

所有数值（时间、容量、速率、桶余量）一律使用 `fractions.Fraction`；
API 边界直接拒绝 float，避免二进制浮点漂移累积。测试里用
`rate = 1/10`（二进制不可表示）连续 1000 步验证：浮点实现会在
`t=10k` 处错误拒绝，本实现逐步精确等于 `Fraction(1)`。

## Revision model

每次编辑（`insert_request` / `update_config`）返回 `RevisionReport`：

- `recomputed_range` — 实际重算的闭区间索引（记录真实工作量）；
- `changed_range` — 覆盖所有结果变化的最小连续区间（下游只需重放
  这段）；
- `changed_request_ids` — 变化的请求。

引擎版本每次有效编辑 +1，只有结果真正变化的记录才获得新版本号；
`changed_range` 之外的记录值与版本号都保持不变，即稳定结果保持身份。

## Why recomputation can stop early

事件之间唯一向后传递的状态是**桶余量**（精确 `Fraction`）和上一事件
时刻；判定是 `(level, prev_time, 剩余事件, 当前时刻起的配置)` 的纯
函数。因此在事件 `i` 处可同时满足两条即可停：

1. **状态重新汇合**：`i` 处重算出的记录与已存记录逐项相同（判定、
   原因、桶前后状态）——余量差异已被抹平；
2. **输入不再有差异**：`i` 之后事件列表未变（纯补录越过插入点后恒
   成立），且新旧配置在 `[t_i, ∞)` 上已知完全一致。

对有界配置修订，越过最后一个差异段后条件 2 成立；若修订触及末尾的
开放段（如"从 t=2 起速率翻倍"），条件 2 永不成立，重算必须跑到日志
末尾——即使沿途记录逐条相同也不能停（`test_config_change_open_ended_segment`
里 `d` 重算不变、`e` 翻转正说明这一点）。

条件 1 何时发生？余量差 δ 经过补充（两边加同样的量）和放行（两边同
减 1）都保持不变；拒绝不消耗令牌，只会让 δ 变号而不消除。**唯一能
抹平历史差异的是容量上限**：长空闲后的补充封顶
（`min(level + rate·Δt, capacity)`）和容量缩小段的边界钳制，都会把
两条历史压到完全相同的余量。这就是为什么补录进繁忙日志通常只需修订
很短的一段连续前缀——也是为什么引擎逐条比较完整状态（判定 + 桶前
后），而不是只比最终桶余量。

推论：纯补录时，变化记录一定构成重算范围头部的单个连续块（第一条
相同的记录即触发停止）；有界配置修订时，重算范围内部可能夹杂"碰巧
相同"的记录（前方配置仍有差异，不能停），所以 `changed_range` 报的
是最小覆盖区间，且可能严格小于 `recomputed_range`。

## Usage

```python
from token_bucket import ReplayEngine

engine = ReplayEngine(
    segments=[(0, 2, 1)],                       # 容量 2，每毫秒补 1
    requests=[(0, 1, "a"), (0, 2, "b"), (0, 3, "c")],
)
# a、b 放行，c 拒绝（0 时刻三次请求只有前两次通过）

report = engine.insert_request(0, 0, "x")       # 补录更早的历史请求
report.changed_range                            # 受影响判定的最小连续范围
report.recomputed_range                         # 实际重算范围

report = engine.update_config([(0, 2, 0), (2, 2, 1)])  # 追溯调整配置
```

下游消费方式见 `revision_consumer.py`：初始全量装载后，每次修订只
重读 `changed_range`，并用 `verify()` 与引擎全量状态对账。
