# fragment-assembler

有限内存的消息分片组装器（TypeScript）。接收携带 `messageId`、总长 `totalLength`
和偏移 `offset` 的分片，允许乱序与重叠重发；同范围字节冲突时隔离（quarantine）
整条消息；超时后同编号消息可作为新代次重新进入。

## 构建与测试

```sh
tsc -p tsconfig.json && node dist/test/assembler.test.js
```

## 核心行为

- **组装**：每条在途消息分配 `totalLength` 字节缓冲 + 接收位图；重叠区域逐字节
  校验，一致则幂等接受（`duplicate`/`accepted`），收齐即 `complete` 并释放内存。
- **冲突隔离**：重叠字节不一致 → 整条消息进入隔离态，缓冲立即释放（保留墓碑），
  同编号后续分片一律返回 `quarantined`，直到代次超时。
- **代次超时**：`now - createdAt >= generationTimeoutMs` 的在途状态（含隔离墓碑）
  被清除，同编号消息作为新代次进入；也可主动调用 `sweep()`。
- **内存上限**：所有在途消息的 `totalLength` 之和不超过 `maxBufferedBytes`，
  超限的新消息被拒绝（已在途消息不受影响）。
- **空消息**：`totalLength = 0` 的分片立即以空负载 `complete`，不占用状态。

## 迟到分片防护（三层）

1. 分片可携带可选的发送侧 `generation` 标签；与在途代次不一致 → `rejected`
   （`stale generation`），这是最严格的保证。
2. 无标签时，`totalLength` 与在途代次不一致 → `rejected`。
3. 总长也相同但字节陈旧时，一旦与新代次已收字节重叠即触发冲突隔离——
   组装器永远不会静默交付被污染的消息。

注意：若迟到分片无 `generation` 标签、总长相同且恰好只填补未收空洞而无任何
重叠证据，任何无代次信息的协议都无法识别；需要严格保证时请使用 `generation`
标签。

## API

- `new FragmentAssembler({ maxBufferedBytes, generationTimeoutMs, now? })`
- `addFragment(fragment): AddResult` — 状态：`complete` / `accepted` /
  `duplicate` / `conflict` / `quarantined` / `rejected`
- `sweep(now?): string[]` — 清理超时代次，返回被驱逐的 messageId
- 只读属性：`usedBytes`、`inFlightCount`、`isQuarantined(id)`
