# quota-protocol

整数 tick 驱动的租借式配额协议。中心按固定窗口签发授予（grant），边缘节点在网络
分区时仍可花已借额度，但未消费额度只有中心确认归还后才能重发。零依赖，需要
Node >= 23.6（原生 TypeScript 类型擦除运行）。

## 运行

```sh
npm test                                    # node:test 单元 + 仿真测试
node src/cli.ts replay --seed 7 --ticks 400 --window 50 --quota 120 \
     --loss 0.1 --dup 0.05 --partition 120-139,243-258 \
     --restart A@170,B@300 --out data/run   # 确定性仿真 + 审计
node src/cli.ts audit --dir data/run        # 仅按账本独立重放审计
```

同一 seed 的 replay 产生字节级一致的账本，可反复审计验证。

## 协议

- **窗口**：`windowId = floor(tick / windowLength)`，授予的 `expiryTick` 为窗口末
  tick。授予携带窗口 ID、节点 ID、grantID、数量、到期 tick。
- **中心不变量**：任意时刻 `spent(w) = issued(w) - confirmedReturned(w) <= quota`。
  只有收到节点的 `ReturnReport` 并确认（`ReturnAck`）后，对应额度才回到可签发池；
  中心**没有**任何超时回收逻辑——收不到回执不能推断额度未用。
- **幂等**：借款按 `requestId` 幂等（重发返回同一 grant，用于对抗授予回执丢失）；
  归还按 `returnId` 去重（重复归还返回同一 ack，超量归还截断到未还余额）；
  消费按 `requestId` 幂等，同 ID 不同成本判为冲突且不再扣减。
- **节点原子性**：消费与归还的竞争由节点本地状态单线程决定——`consume` 同步扣
  `remaining` 并落账本，`sweep` 只归还当时剩余的未消费部分。`tick == expiryTick`
  可消费，`expiryTick < tick` 一律不可用；迟到授予（送达时已过期）不复活预算，
  立即全额归还。
- **持久化**：中心与每个节点各维护一份 append-only JSONL 账本
  （`center.ledger.jsonl`、`node-<id>.ledger.jsonl`），重启即重放恢复，包括未确认
  归还的重传与消费幂等表。

## 审计（独立汇总）

`src/audit.ts` 只读账本、不碰活状态，重放全部 `consume_accepted` 事件：同一
`requestId` 只计一次，校验每窗总消费 ≤ 额度、消费 ≤ 签发、中心 spent 逐事件不超
额度、没有消费动用已过期授予；输出每窗闲置配额（`quota - consumed`）、拒绝原因
分类（`insufficient_quota` / `window_quota_exhausted` /
`request_id_cost_conflict`）与成本冲突清单。

## 仿真场景

`src/simulation.ts` 用种子化 PRNG 驱动消息丢失、重复、延迟与网络分区，覆盖：
授予回执丢失（借款重试幂等恢复）、节点重启（账本恢复 + 归还重传）、重复归还
（总线重复 + 中心去重）、截止 tick 消费（窗口末 tick 仍可用）、窗口切换（旧授予
到期归还、新窗额度独立）、跨窗口边界的分区导致的迟到授予。

## 文件

- `src/types.ts` — 消息与授予类型
- `src/center.ts` — 中心：签发、归还确认、幂等借款
- `src/node.ts` — 节点：幂等扣减、到期归还、迟到授予退回、崩溃恢复
- `src/bus.ts` / `src/simulation.ts` — 确定性网络与场景驱动
- `src/audit.ts` — 独立审计器
- `src/cli.ts` — `replay` / `audit` CLI
- `test/` — node:test 测试（17 个用例）
