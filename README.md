# pbwire-snapshot

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 增量快照 / 检查点（Checkpoint）归档引擎（TypeScript，Node.js 内置测试运行器验证）。

## 组件

- `src/varint.ts` — Varint（7 位压缩）、ZigZag 有符号映射、Fixed32/64 小端编解码；所有读取严格边界检查，截断抛 `TruncatedFieldError`，超长 varint 抛 `MalformedVarintError`。
- `src/wire.ts` — `WireReader` / `WireWriter`：Tag 解析、字段跳过、Length-delimited 子消息切分、废弃 wire type（SGROUP/EGROUP）拒绝。
- `src/schema.ts` — 反射模式校验引擎：声明式字段描述符驱动编解码，支持多层嵌套消息、Packed Repeated 数组、未知字段安全跳过、编码前结构校验。
- `src/snapshot.ts` — 增量快照与检查点归档：
  - `VersionedStore`：MVCC 式状态库，单调版本号 + 脏键集合；变更同步完成，与快照临界区天然互斥。
  - `SnapshotEngine.snapshot()`：仅捕获上次快照以来的脏条目（低停顿：脏集合在同步临界区内一次性换出，不阻塞变更）。
  - `SnapshotEngine.checkpoint()`：全量基线 + 增量链折叠为**自描述 protobuf 二进制归档**（用本包自有编解码器序列化，自举验证）。
  - `SnapshotEngine.restore()`：校验 magic / 格式版本 / 增量链版本连续性，重放到全新状态库。
  - 并发安全：异步互斥锁（promise 链，无忙等）串行化快照/检查点；泄漏防护：`ResourceRegistry` 严格记账，`assertNoLeaks()` / `dispose()` 强制全部释放。

## 运行

需要 Node.js ≥ 23.6（内置 TypeScript 类型擦除，无需编译、无依赖安装）：

```sh
npm test        # node --experimental-strip-types --test test/*.test.ts
npm run example # 端到端样例：嵌套消息编解码 + 快照 + 归档恢复
```

或直接：

```sh
node --experimental-strip-types --test test/*.test.ts
node --experimental-strip-types examples/demo.ts
```

## 测试覆盖

- `test/varint.test.ts` — varint 规范向量 / 7 位边界（0..64 位）/ zigzag 极值 / fixed32/64 / 截断与溢出防护。
- `test/wire.test.ts` — 三层嵌套消息、packed 与非 packed repeated 混合解码、未知字段跳过、逐字节截断安全性、错误 wire type / 非法 tag 拒绝、编码前校验。
- `test/snapshot.test.ts` — 空快照、脏条目增量、删除墓碑、热点键折叠、检查点归档往返、未快照变更冲刷、归档损坏/截断拒绝、增量链缺口检测、并发快照链连续性、检查点期间变更不丢失、并行检查点一致性、全生命周期严格无泄漏。
