# reconcile — 状态历史对账工具

从多来源日志重建业务对象的最终状态。日志允许**缺项**、**重复**和**不准的时间戳**，
但事件保留**因果引用**（`causes`）。工具枚举所有合法重建，给出最终状态集合；
只有集合唯一时才给出确定结论。发现矛盾时返回最小事件子集。

## 用法

```sh
go build -o reconcile .
./reconcile events.json        # 或 cat events.json | ./reconcile
```

退出码：`0` = determined/ambiguous，`2` = contradiction，`1` = 输入错误。

## 输入

JSON 数组（或 `{"events": [...]}`），最多 **20** 个事件（去重后）：

```json
{
  "id": "e2",                       // 必填，事件唯一标识
  "source": "billing-log",          // 可选，来源（仅展示用）
  "timestamp": "2026-01-03T10:00:01Z", // 可选，不可信，绝不用于排序
  "object": "order-42",             // 必填，业务对象 ID
  "op": "set",                      // create | set | delete
  "fields": {"status": "paid"},     // create/set 的字段
  "causes": ["e1"]                  // 因果前驱：这些事件必须发生在本事件之前
}
```

## 语义

- **因果优先**：合法重建是因果偏序的线性扩展。时间戳不参与排序——
  按时间戳排序再忽略因果是禁止的。
- **缺项容忍**：`causes` 引用日志中不存在的事件是允许的（在
  `missing_causes` 中报告）；对象的首个观测事件可以是任意操作
  （此前历史可能缺失）。
- **重复容忍**：同一事件（同 ID 同内容）被多个来源重复上报时去重；
  同 ID 但内容冲突是矛盾（`conflicting_duplicates`）。
- **操作合法性**：日志内部不允许对存活对象再次 `create`，也不允许对
  已删除对象 `set`。`delete` 幂等。
- **矛盾与最小子集**：
  - `causal_cycle`：因果引用成环，返回最短环（最小基数子集）；
  - `conflicting_duplicates`：同 ID 冲突记录对；
  - `no_legal_reconstruction`：不存在使全部操作合法的因果一致序，
    返回包含极小的不可行事件子集。

## 输出

```json
{
  "status": "determined | ambiguous | contradiction",
  "event_count": 3,
  "duplicates_removed": 1,
  "legal_orders": 2,
  "missing_causes": {"e3": ["e0-missing"]},
  "conclusion":  { "...": "..." },   // 仅 determined：唯一最终状态
  "final_states": [ "..." ],         // 仅 ambiguous：全部可能最终状态
  "conflict": {"kind": "...", "events": ["..."]}  // 仅 contradiction
}
```

`legal_orders` 是合法重建（线性扩展）的总数；注意多个合法序可能收敛到
同一最终状态——判定唯一性看的是**最终状态集合**，不是序的个数。

## 示例

```sh
./reconcile examples/determined.json   # 时间戳乱序但因果确定唯一结论
./reconcile examples/ambiguous.json    # 两个无因果序的写 → 两个最终状态
./reconcile examples/cycle.json        # 因果环 → 最小矛盾子集 [a b c]
```

## 实现

`reconcile.go`：因果图 + 分层子集 DP（`2^20` 掩码上枚举所有合法线性扩展，
对象状态 intern 为整数，世界状态是有序整数切片）。最坏情况（20 个完全
独立事件）约 7 秒。`main.go`：CLI。`reconcile_test.go`：语义测试。
