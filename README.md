# fragment-reassembler

有限内存的消息分片组装器（TypeScript）。接收携带总长与偏移的分片，允许重叠重发；
相同范围字节不一致时隔离（quarantine）整条消息；超时后允许同编号的新代次进入，
迟到分片不会混入新消息。

## 运行测试

需要 Node.js ≥ 22.6（依赖内置 TypeScript 类型擦除与 `node:test`）：

```sh
node --test
```

## 用法

```ts
import { Reassembler } from './src/reassembler.ts';

const r = new Reassembler({ maxBytes: 1 << 20, timeoutMs: 30_000 });

const res = r.addFragment({
  id: 'msg-1',
  generation: 0,      // 可选，默认 0
  totalLength: 11,
  offset: 0,
  data: new TextEncoder().encode('hello '),
});

if (res.status === 'complete') {
  // res.data 为组装完成的完整消息
}
```

## 行为约定

- **重叠重发**：重叠区域字节必须完全一致，否则整条消息被隔离；
  `totalLength` 不一致同样视为冲突。
- **隔离**：冲突后消息进入 quarantined 状态，缓冲区立即释放，同代分片一律拒绝，
  直到状态超时后才允许同编号重新开始。
- **代次（generation）**：新代次仅在旧状态已完成或超时后被接纳；
  旧代次的迟到分片返回 `stale`，不会写入新消息。
- **内存上限**：按 `totalLength` 预分配并计入 `usedBytes`，总量恒不超过 `maxBytes`；
  空间不足时逐出最久未活动的未完成消息，单条消息超过 `maxBytes` 直接拒绝。
- **结果类型**：`complete` / `pending` / `duplicate` / `stale` / `rejected`
  （`invalid` | `conflict` | `quarantined` | `generation-locked` | `memory`）。

## API

- `addFragment(fragment): AddResult` — 摄入一个分片。
- `sweep(): number` — 主动清理超时状态（读取路径上也会惰性过期）。
- `has(id)` / `isQuarantined(id)` — 状态查询。
- `stats(): ReassemblerStats` — 消息数与内存用量。
