# 多分区确定性事务状态机

基于本地确定性日志的多分区键值状态机（TypeScript，零依赖，Node.js 内置测试）。

## 设计

- **确定性日志**：所有状态变更由带全局序号 `seq` 的日志事件驱动；乱序/重复到达的事件先进入有界重排缓冲，严格按 `seq` 连续回放，因此同一日志流必然产生同一最终状态。
- **两阶段回放**：`prepare` 登记未决事务，`commit`/`abort` 裁决；写集按分区、键排序应用。
- **依赖拓扑排序**：commit 时对冲突事务构建依赖图（`seq` 小 → `seq` 大），Kahn 算法 + `(seq, txnId)` 平局规则，排序结果确定（`src/topo.ts`）。
- **可审计决胜证据**：未决冲突按「日志序先 commit 者胜，未决冲突方中止」裁决，证据含胜者/负者、拓扑序、键级冲突明细与规则（`src/stateMachine.ts` 的 `TieBreakEvidence`）。
- **内存有界**：重排缓冲（`maxBuffer`）与未决事务表（`maxPending`）均有上限，超限确定性驱逐最旧未决事务并留证；快照后截断已回放日志。
- **崩溃恢复**：`createSnapshot()` 在切片点导出分区状态与未决事务；`StateMachine.recover(snapshot, events)` 从快照重放切片点之后的日志（允许乱序与旧事件，幂等）。

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，无需编译）。

```sh
npm test     # node --test（内置测试运行器）
npm run demo # 运行验收样例
```

## 文件

- `src/types.ts` — 日志事件 / 证据 / 快照类型
- `src/stateMachine.ts` — 状态机核心（回放、决胜、快照、恢复）
- `src/topo.ts` — 确定性拓扑排序
- `src/scenario.ts` — 验收场景（T1/T2 跨 2 分区并发交错）与乱序注入
- `test/stateMachine.test.ts` — 7 个验收测试
- `examples/demo.ts` — 运行样例

## 测试结果

```
✔ 乱序网络包：并发交错的 T1/T2 产生相同最终状态
✔ 未决冲突：输出可审计决胜证据且内容确定
✔ 快照切片点重放：快照 + 后续日志 == 全量回放
✔ 崩溃恢复：空快照 + 全量乱序日志 == 顺序回放
✔ 内存有界：未决事务表超限确定性驱逐并留证
✔ 重复与过期事件幂等
✔ 显式 abort 不产生写
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

## 样例输出（`npm run demo` 摘要）

```
=== 1. 乱序网络包下的确定性 ===
投递顺序(seq): 2 0 5 10 9 11 3 1 6 8 2 4 7
顺序回放终态: {"0":{"a":"1","hot":"T5","shared":"T1"},"1":{"w":"5","x":"1","z":"3"}}
乱序回放终态: {"0":{"a":"1","hot":"T5","shared":"T1"},"1":{"w":"5","x":"1","z":"3"}}
终态一致: true    提交顺序一致: true [ 'T1', 'T3', 'T5' ]

=== 2. 未决冲突的可审计决胜证据 ===
{ "kind": "conflict-tiebreak", "decidingSeq": 2, "winner": "T1", "losers": ["T2"],
  "rule": "commit-in-log-order-wins; ...", "topoOrder": ["T1","T2"],
  "conflicts": [ {"partition":0,"key":"shared","kind":"write-write"}, ... ] }

=== 3. 快照切片点 + 崩溃恢复 ===
切片点 nextSeq=6，截断后保留日志=0 条
在线实例终态 == 崩溃恢复终态 == 全量回放终态: true
```
