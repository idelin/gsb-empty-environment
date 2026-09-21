# rlh — 带来源索引的设备状态运行长度编码

把连续相同状态的设备事件压缩成**半开区间**段 `[start, end)`，每个段保留构成它的
**每一条原始事件** `(source, seq)`。支持插入、撤回、更正三种编辑，均输出增量
`Patch`；下游只拿“初始段 + 补丁流”即可重放出与直接查询完全一致的结果。

要求 Python 3.11+，无第三方运行依赖（测试用 pytest）。

## 语义

- **时间**：整数。段为半开区间 `[start, end)`，最后一个段 `end=None`（延续到无穷）。
- **身份**：`Record(time, state, source, seq)`，`(source, seq)` 全局唯一。
- **合并**：同一胜者状态在相邻时间连续出现则合并为一个段；段内 `sources` 保留全部
  证据（含重复断言、被合并的中间记录），合并不丢证据。
- **同时间冲突**：同一时间出现不同状态时，由 resolver 裁决（默认 `seq` 大者胜，
  `seq` 相同再比 `source`），败者不进段但可通过 `conflicts()` 查到。
- **段身份**：未变化的段保持 `sid` 稳定；撤回若恢复旧结构，复用原 `sid`。
- **更正**：对同一 `(source, seq)` 的原子“撤回+插入”，只发一个补丁，无中间可见态。

## 接口

查询与编辑：`HistoryStore`

- `insert(record)` / `retract(source, seq)` / `correct(record)` → `Patch`
- `segments()` / `segment_at(t)` / `trace(sid)` / `trace_at(t)`
- `records()` / `record(source, seq)` / `conflicts()` / `conflict_at(t)`

补丁消费：`SegmentedView(initial_segments)`，`apply(patch)` / `apply_all(patches)`，
对外暴露与 store 相同的 `segments()` / `segment_at()` / `trace()`。

纯函数（用于与原始事件重算核对）：`build_runs(records)`、`find_conflicts(records)`。

`Patch` 含 `removed`（按 `sid` 先删）与 `added`（按 `start` 插入），空补丁表示该编辑
（例如撤回同时间冲突中的败者）不影响段流。

## 两条查询路径与核对

测试对每一步同时比较三种结果：

1. `store.segments()` —— 直接查询当前段；
2. `SegmentedView` 从初始段应用全部补丁；
3. `build_runs(raw_records)` —— 从原始事件流重算。

随机属性测试（40 个种子、插入/撤回/更正混合）保证三者内容逐字段一致，并校验半开
端点、来源集合与补丁引用的段 id 均合法。

## 运行

```bash
python -m pytest -q          # 测试
python -m examples.consumer_demo   # 补丁消费示例
```
