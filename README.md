# LSM Column Families

单 LSM 实例托管多个逻辑隔离的列族（Column Family），TypeScript 实现，
零依赖，使用 Node.js 内置测试运行器（Node ≥ 23.6，原生类型擦除直接运行 `.ts`）。

## 架构

| 组件 | 文件 | 职责 |
| --- | --- | --- |
| `Engine` | `src/engine.ts` | 多列族管理、全局序列号、原子多列族写入、快照、恢复 |
| `ColumnFamily` | `src/column-family.ts` | 私有 MemTable + 私有 SSTable 元数据列表 |
| `MemTable` | `src/memtable.ts` | 带版本（seq）的内存写缓冲，支持快照可见性 |
| `SsTable` | `src/sstable.ts` | 不可变有序磁盘表，按列族独立生成 |
| `Wal` | `src/wal.ts` | **所有列族共享的唯一物理日志** |

## 关键设计

- **共享 WAL**：`writeBatch` 把跨列族的所有操作编码为**单条** WAL 记录
  （`长度:校验和:JSON` 帧 + fsync），要么整体重放、要么整体丢弃 —— 多列族写入原子性。
- **恢复对齐**：重放以批次为边界，两个列族永远同步对齐到同一事务点；
  崩溃产生的撕裂尾记录由长度 + SHA-256 校验和检测并整体忽略。
- **独立快照**：`snapshot(cf)` 固定该列族在某一全局序列号的视图，
  其他列族的写入不影响它。
- **MANIFEST**：记录列族创建与各列族独立的 flush 元数据，重开时重建。

## 运行

```bash
npm test    # node --test test/*.test.ts（12 个用例）
npm run demo
```

## 验收边界对应测试（`test/column-family.test.ts`）

- 创建 `CF_Default` / `CF_Metadata` → `column family management`
- 单次原子事务写两个列族 → `atomic multi-CF writes`
- `CF_Metadata` 只能看到本列族数据 → `isolation between families`
- 恢复时两列族原子同步对齐（含撕裂写保护）→ `recovery from the shared WAL`
