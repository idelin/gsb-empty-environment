# 成员切换模拟器（联合共识 / Joint Consensus）

模拟 Raft 风格的两阶段成员变更：集群从旧组切换到新组时，必须先进入
**联合期（joint）**，该阶段内任何条目（包括配置项本身）都必须同时获得
**旧组多数** 与 **新组多数** 才能提交；联合配置提交后才能发起第二阶段，
提交 `C_new` 完成切换。由此保证新旧两个多数派必然相交，切换不能跳过
中间确认。

## 配置状态机

```
stable(old) --enter-joint(C_old,new)--> joint(old, new) --leave-joint(C_new)--> stable(new)
```

- `src/config.ts` — 配置状态、`applyConfigCommand` 状态机（非法跳步直接抛错）、
  `quorumReached` 提交判定（稳定期 1 个多数组，联合期 2 个多数组各自独立达多数）。
- `src/cluster.ts` — 单领导者确定性模拟器：日志提案、配置提案（同一时刻只允许
  一个未提交的配置变更）、`replicateTo` 显式复制（未送达即分区）、
  `advanceCommitIndex` 按条目各自携带的配置做提交判定。
- 配置项本身也是日志条目，按其所处阶段的规则提交后，才推进节点的
  `committedConfig`（已提交配置状态机）。

## 反例

`src/joint-consensus.test.ts` 中：

1. **反例（跳过中间确认）**：旧组 `{A,B,C}` 直接切到新组 `{D,E,F}`，
   配置项只复制到旧组多数 `{A,B}` 即按旧规则“提交”。新组多数派
   `{D,E,F}` 与 `{A,B}` 不相交，对该已提交配置项一无所知，可自行选出
   领导者 D —— 出现两个互不认可的领导者，已提交配置可能丢失。
2. **联合共识阻止反例**：同样的复制范围（旧组达多数、新组不能）下，
   `C_old,new` 无法提交，状态机拒绝发起 `leave-joint`；即使进入联合期，
   `C_new` 也不能仅凭旧组多数提交。切换被安全地阻塞，而非跳步。

## 运行

```sh
npm test          # node --test（Node >= 23，原生运行 TypeScript）
npm run typecheck # 需要本机安装 typescript
```
