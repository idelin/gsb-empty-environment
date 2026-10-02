# pbwire-snapshot

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎 + 增量快照/检查点（Checkpoint）归档引擎。纯 TypeScript，使用 Node.js 内置测试运行器验证。

## 环境要求

- Node.js **>= 23.6**（原生 TypeScript 类型擦除，无需 `tsc`/任何依赖；仓库附带 `.nvmrc`）

## 运行

```bash
npm test    # node --test test/*.test.ts（51 个用例）
npm run demo
```

## 组件

| 模块 | 说明 |
| --- | --- |
| `src/varint.ts` | Varint 7 位压缩编解码（64 位 BigInt）、ZigZag 有符号转换；截断/溢出安全报错 |
| `src/wire.ts` | `WireReader`/`WireWriter`：Tag、Fixed32/64（小端）、Length-delimited、嵌套子 reader，全程边界检查 |
| `src/schema.ts` | 反射式 schema 描述与校验引擎（`validateMessage`），违规携带完整字段路径 |
| `src/message.ts` | schema 驱动的消息编解码：多层嵌套消息、Packed Repeated、未知字段安全跳过/保留 |
| `src/snapshot.ts` | 增量快照 + 检查点归档：Promise 队列串行化（并发安全）、分块拷贝让出事件循环（低停顿）、防御性拷贝、`dispose()` 零泄漏 |

## 设计要点

- **安全越界防护**：所有读取经 `WireReader` 边界检查；截断的 varint / fixed / length-delimited / 嵌套消息一律抛 `DecodeError`（带偏移量），绝不越界读。
- **低停顿快照**：增量快照只拷贝自上次快照/检查点以来的操作日志；每 `chunkSize` 条让出一次事件循环。
- **并发安全**：所有变更与归档操作经内部 Promise 链全序串行化，版本号严格递增。
- **无泄漏**：归档数据全部防御性拷贝；`dispose()` 清空全部结构，`stats().liveResources` 归零，测试严格断言。
