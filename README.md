# 整数 tick 驱动的窗口配额协议模拟

两个边缘节点向中心借用配额。网络分区时节点只能花已借到的额度，无法各自重新生成全部额度。纯 TypeScript、零依赖，使用 Node 内置测试与确定性回放 CLI。

## 运行

需要 Node >= 23.6（原生 TypeScript 类型擦除）。`tools/node23.sh` 会自动选择解释器。

```bash
npm test          # node:test 单元测试 + 演示场景审计
npm run demo      # 内置分区场景：完整 transcript + 审计 + 确定性自检
npm run replay -- --scenario scenarios/partition.json --verbose --check
```

CLI：`node src/cli.ts replay [--scenario file.json] [--data dir] [--verbose] [--check]`
审计失败（违规/重复 ID）或确定性自检不一致时退出码非 0。

## 协议语义

- tick 为非负整数；窗口 `w` 覆盖 `[w*L, (w+1)*L - 1]`，到期 tick **含端点**（`tick <= expiresAtTick` 可消费）。
- 中心按当前窗口签发授予 `{windowId, nodeId, grantId, amount, expiresAtTick}`，expiry 为窗口末 tick，不得跨窗。
- 中心不变式：任一窗口恒有 `grossIssued(w) - confirmedReturned(w) <= budget(w)`（`src/center.ts`）。
- 节点 `spend(requestId, cost, tick)` 幂等：同 ID 同成本返回缓存决策；同 ID 不同成本记 `spend-conflict`，状态不变（`src/node.ts`）。
- 归还与消费的竞争由节点本地原子状态决定：`returnGrant` 先把授予冻结为 `returned`，之后的 `spend` 立即看不到它；先到的消费则先从余额中扣除。
- 未消费额度只有在中心确认归还（`return` → `return-confirm`）后才回到可用池；中心没有任何基于超时的回收路径，丢失/分区中的授予只会成为闲置配额。
- 重复归还幂等：节点重发同一 `returnId`/金额，中心按 grantId 去重，按首次确认金额重新确认，不重复计入。
- 过期额度不可跨窗使用；迟到授予（到达时 `expiresAtTick < tick`）只入账、永不可消费，不能复活过期预算。

## 持久化与重启

中心与每个节点各持有一份追加式 JSONL 账本（`out/run/center.jsonl`、`out/run/node-<id>.jsonl`），先写盘后应用（WAL）。重启 = 丢弃进程内状态、从账本重放，授予、消费、幂等请求表、归还确认全部恢复。

## 独立审计

`src/audit.ts` 只读账本文件重建事实：

- 逐条重放中心日志，验证每时刻净签发 ≤ 窗口额度、expiry 不跨窗、归还金额合法、重复归还不重复计入；
- 汇总所有接受的请求，按 requestId 全局去重（同 ID 只计一次，跨节点重复记为异常）；
- 校验分摊来自已收到、未归还、未过期的授予且不超余额；
- 输出每窗 `consumed <= budget`、`idle = budget - consumed`、拒绝原因统计、未确认归还、从未送达的授予。

## 演示场景（`src/scenario.ts` / `scenarios/partition.json`）

窗口长 100、窗额 400、节点 n1/n2，260 tick 内覆盖：授予回执丢失（G2）、分区中签发被丢弃（G3）、分区中花已借额度、节点重启后幂等重放、余额不足拒绝、归还消息丢失（超时不回收）、确认丢失后重发→中心幂等去重、确认后重发（G4/G8）、截止 tick（t=99）消费、窗口切换旧额度失效、中心重启、迟到授予（G6，t=251 到达）不能复活过期预算。

场景 op：`issue` / `spend` / `return` / `returnAll` / `restart` / `partition` / `heal` / `drop`，`issue` 支持 `delay` 制造迟到授予。

## 目录

- `src/types.ts` 公共消息类型；`src/ledger.ts` JSONL 账本
- `src/center.ts` 中心；`src/node.ts` 边缘节点
- `src/sim.ts` tick 驱动模拟器（分区/丢包/重启）；`src/audit.ts` 独立审计
- `src/cli.ts` 回放 CLI；`src/scenario.ts` 内置演示场景
- `test/protocol.test.ts` 15 个 node:test 测试
