# proto-wire-faultlab

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎 + 异常注入/崩溃恢复容错验证（TypeScript，Node.js 内置测试运行器）。

## 运行

需要 Node.js >= 22.6（利用 `--experimental-strip-types` 直接运行 TypeScript，无任何构建步骤与第三方依赖）。

```bash
npm test     # 运行全部测试（node:test）
npm run demo # 运行演示样例
```

## 组件

| 模块 | 职责 |
| --- | --- |
| `src/varint.ts` | Varint 7 位压缩编解码、ZigZag 有符号转换、64 位溢出/截断防护 |
| `src/wire.ts` | Wire 层 `Writer`/`Reader`：Varint、Fixed32/64、Length-delimited、tag 校验、未知字段跳过，全程边界检查 |
| `src/schema.ts` | 反射校验引擎：按 schema 编解码嵌套消息、packed/unpacked repeated、必填校验、wire type 匹配校验 |
| `src/crc32.ts` | 表驱动 CRC-32 (IEEE)，日志记录完整性校验 |
| `src/fault.ts` | 故障注入：`FaultySink`（断电撕裂写 / 磁盘满）、`LossyChannel`（确定性丢包） |
| `src/journal.ts` | 预写日志（WAL）：`[len][crc32][payload]` 记录，崩溃恢复时自愈截断撕裂尾部 |
| `src/store.ts` | 事务型 KV：WAL 先行 + 失败全量回滚 + `AsyncMutex` 并发串行化 + staged 缓冲泄漏追踪 + 丢包信道可靠传输 |

## 容错语义

- **突发断电**：`FaultySink` 在写入中途只落盘部分字节后抛 `POWER_LOSS`；`Journal.recover()` 顺序扫描并校验 CRC，将文件截断到最后一条完好记录（自愈），半截事务永不应用。
- **磁盘满**：append 抛 `DISK_FULL`，`TransactionalStore.commit` 整体回滚，内存状态保持提交前快照，staged 缓冲在 `finally` 中全部释放（`stagedBufferCount` 断言为 0）。
- **网络丢包**：`LossyChannel` 按脚本丢弃分片，`reliableTransfer` 逐分片确认 + 自动重传，超过重传上限抛 `PACKET_LOSS`。
- **越界防护**：`Reader` 所有读取先校验剩余长度，字段截断一律抛 `TRUNCATED`（携带字节偏移与字段路径），绝不越界访问。

## 测试

`test/` 下 4 个文件共 35 个用例，覆盖：varint/zigzag 边界值与溢出、任意截断点模糊测试（只可能合法解码或 `TRUNCATED`）、三层嵌套 + packed repeated 精确还原、必填/wire type 校验、断电自愈、磁盘满回滚、丢包重传、并发提交串行化与无泄漏断言。
