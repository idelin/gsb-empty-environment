# ARIES Crash Recovery Engine (TypeScript)

数据库经典 ARIES 崩溃恢复协议的 TypeScript 实现：预写日志（WAL）分析、
Redo/Undo 三阶段恢复、紧凑二进制持久化编解码与内存映射式零拷贝还原。
仅依赖 Node.js 内置模块（`node:test` / `node:fs` / `node:buffer`），
Node >= 22.6 直接以类型擦除方式运行 `.ts`，无需构建步骤。

## 运行

```bash
npm test        # node --test test/*.test.ts（40 个测试）
npm run demo    # 构造崩溃现场并执行三阶段恢复，打印全过程
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/binary.ts` | 有界读写器、LEB128 varint、CRC-32；越界一律抛 `CorruptionError` |
| `src/logrecord.ts` | 版本化日志记录编解码（UPDATE/CLR/COMMIT/ABORT/END/BEGIN/END_CHECKPOINT） |
| `src/wal.ts` | WAL 帧写入器（异步互斥串行化）、内存映射式扫描器、按 LSN 随机访问 |
| `src/pagestore.ts` | 页式存储 + pin/unpin 缓冲池（泄漏检测、脏页表/recLSN） |
| `src/recovery.ts` | ARIES 三阶段：Analysis / Redo（历史重复）/ Undo（CLR 补偿） |
| `src/minidb.ts` | 遵循 WAL 协议的迷你事务引擎，用于构造崩溃场景 |
| `src/scenario.ts` | 验收场景：3 个未提交 + 2 个已提交事务，Checkpoint 后崩溃 |

## 二进制格式

文件头（16 字节）：`u32 magic "ARW1"`、`u16 formatVersion`、`u16 headerSize`、
`u16 flags`、保留 6 字节。之后是帧序列：

```
frame  := u32 payloadLen, u32 crc32(payload), payload[payloadLen]
record := u8 version, u8 type, u64 lsn, u32 txnId, u64 prevLSN, body...
```

LSN 即帧在文件中的字节偏移，因此 LSN 可直接用于随机访问
（`readRecordAt`）。页面前 8 字节存 `pageLSN`。

### 兼容性

- **向后兼容**：`version` 字段门控解析；未知记录类型解码为 `UNKNOWN`
  并被扫描器跳过；文件头版本高于支持版本时抛 `UnsupportedVersionError`。
- **向前兼容**：更高记录版本按 v1 前缀解析，帧长前缀界定的未知尾部
  字段被安全忽略。
- **边界防溢出**：所有读取经 `BoundedReader` 边界检查；varint 拒绝
  超过 64 位的编码；崩溃造成的撕裂尾（torn tail）视为日志逻辑末尾，
  `strict` 模式下则抛 `CorruptionError`；CRC 校验逐帧进行。

### 零拷贝还原

`mapWal` 把整个 WAL 读入单个连续 Buffer（内存映射代理），扫描与解码
产生的 before/after image 均为该 Buffer 的 `subarray` 视图，不复制
任何负载字节；测试以 `view.buffer === mapped.buffer` 断言别名关系。

## 恢复三阶段

1. **Analysis**：定位最后一个 `END_CHECKPOINT`，以其 TT/DPT 快照为起点
   向前扫描到日志末尾，重建事务表（running/committed/aborting + lastLSN）
   与脏页表（pageId + recLSN）。
2. **Redo**：从最小 recLSN 起向前扫描，对 DPT 中的页面重放 UPDATE/CLR
   的 after-image（以 `pageLSN < lsn` 为守卫），历史重复、幂等。
3. **Undo**：按最大 lastLSN 优先反向扫描所有 loser 事务，对每个 UPDATE
   写 CLR（`undoNextLSN` 指向下一待撤销记录）并应用 before-image；
   链走到 0 时写 END。崩溃中已写的 CLR 通过 `undoNextLSN` 跳过，
   绝不重复撤销。

## 验收场景（`npm run demo`）

T1/T3/T5 未提交（T5 崩溃时正处于 abort 中途、已写一条 CLR），
T2 已提交（END 已写），T4 已提交（END 因崩溃丢失）。Checkpoint 之后
崩溃；页 1/2 曾被刷盘（含未提交数据的 steal），页 2/3 的后续修改丢失。
恢复后：已提交的 B、E 持久化，未提交的 A、C、D、F、G、H 全部回滚，
再次恢复幂等（无 loser、无新 CLR）。

## 测试覆盖（40 个用例）

- **编解码边界**：varint 全桶边界与超 64 位拒绝、u64 边界、空/64KiB
  镜像、截断负载、版本 0 拒绝、未来版本容忍、未知类型跳过。
- **WAL**：LSN 单调且等于帧偏移、撕裂尾容忍与 strict 检测、CRC 损坏、
  恢复后续写截断、坏 magic、未来文件版本、关闭后拒绝写入。
- **零拷贝**：解码视图与源缓冲区别名断言。
- **恢复**：验收场景全断言（TT/DPT、redo 跳过/重放集合、CLR/END 计数、
  磁盘最终字节、pageLSN 不变式、CLR 链完整性、幂等重恢复）、无
  checkpoint 场景、空 WAL。
- **并发安全**：8 工作者并发追加无撕裂帧、多事务 prevLSN 链一致、
  缓冲池并发 pin/unpin 平衡。
- **无泄漏**：缓冲池 `assertNoLeaks`（pin 计数归零）、句柄关闭标志、
  帧缓存无重复分配。
