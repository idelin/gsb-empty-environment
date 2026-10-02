# LSM 列族（Column Family）隔离与多表管理

单个 LSM 实例托管多个逻辑隔离的列族：每个列族拥有独立的 MemTable 与
SSTable 元数据，所有列族共享同一份物理 WAL，从而支持原子多列族写入与
对齐的崩溃恢复。

## 架构

```
LSMEngine (一个实例)
├── wal.log                 ← 唯一物理 WAL，所有列族共享
├── sst/
│   ├── CF_Default-1.sst    ← 各列族独立的 SSTable 文件与元数据
│   └── CF_Metadata-1.sst
├── CF_Default  → 独立 MemTable + SSTable 列表
└── CF_Metadata → 独立 MemTable + SSTable 列表
```

- `src/engine.ts` — `LSMEngine`：列族管理、读写、快照、flush、恢复
- `src/column-family.ts` — 列族句柄与每列族私有状态（MemTable + SSTable 元数据）
- `src/memtable.ts` — 带版本链（seq）的内存写缓冲，支持快照读
- `src/sstable.ts` — 不可变有序表，二分 + 版本可见性查询
- `src/wal.ts` — 共享 WAL：事务记录 + commit 标记，恢复时丢弃未提交事务

## 关键语义

- **隔离**：`get(handle, key)` 只查该列族的 MemTable 与 SSTable。
- **原子多列族写入**：`writeBatch(ops)` 把全部操作以同一 txn id 追加到
  共享 WAL，再写 commit 标记；要么全部可见，要么全部不可见。
- **独立快照**：`snapshot(handle)` 基于全局序列号，返回该列族的
  点-in-time 视图，且不能跨列族使用。
- **恢复对齐**：重放共享 WAL 时仅应用带 commit 标记的事务；崩溃残留的
  半事务对所有列族一并丢弃，保证各列族原子同步对齐。

## 运行

需要 Node.js ≥ 22.6（利用内置 TypeScript 类型擦除，无构建步骤、无依赖）。

```bash
npm test    # Node.js 内置测试运行器 (node:test)
npm run demo
```

## 验收边界对应

| 验收项 | 测试 |
| --- | --- |
| 创建 CF_Default 与 CF_Metadata | `create CF_Default and CF_Metadata in one LSM instance` |
| 单次原子事务写两个列族 | `single atomic transaction writes to both column families` |
| CF_Metadata 只能看到本列族数据 | `column families are logically isolated` |
| 恢复时两列族原子同步对齐 | `recovery from shared WAL aligns both column families atomically`、`crash mid-batch: uncommitted txn is discarded for ALL column families` |
