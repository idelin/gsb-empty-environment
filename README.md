# LSM Tombstone：删除语义管理内核

LSM 树的删除语义实现：删除操作写入特殊 **Tombstone（墓碑）标记**；读路径
（Point Get / Range Scan）正确屏蔽旧数据；Compaction 在记录下沉至
**Bottom-most 层**、确认不存在更老版本后，安全物理擦除 Tombstone，释放空间。

## 设计

```
put(K, V)    ──► MemTable 追加 {K, seq, V}
delete(K)    ──► MemTable 追加 {K, seq, TOMBSTONE}   （与写入同价）

Point Get    ──► MemTable → L0 → L1 … 逐层查找，首个命中即最新版本；
                 命中 TOMBSTONE 视为 NotFound
Range Scan   ──► K 路归并全部数据源，每 Key 取 seq 最大者，过滤 TOMBSTONE

Compaction   ──► 层内表数超阈值 → 与下一层键范围重叠的表归并（leveled）；
                 归并目标为 Bottom-most 层时丢弃 TOMBSTONE —— 此时更深层
                 不可能存在更老版本，物理擦除是安全的
```

关键不变式：**只有归并目标为最底层时才允许丢弃 Tombstone**。中间层归并若
提前丢弃，更深层残存的旧值会“复活”。本实现通过 `compactLevel()` 中的
`dropTombstones = targetIndex === maxLevels - 1` 严格保证。

## 目录结构

- `src/tombstone.ts` — Tombstone 标记与类型
- `src/entry.ts` — 记录结构（key / seq / value）
- `src/memtable.ts` — 写入缓冲
- `src/sstable.ts` — 不可变有序表（二分查找）
- `src/merge.ts` — K 路归并迭代器（Scan 与 Compaction 共用）
- `src/lsm-tree.ts` — LSMTree 主组件（读写路径 + 分层压缩）
- `test/lsm-tree.test.ts` — Node.js 内置测试（`node:test`）
- `examples/demo.ts` — Tombstone 全生命周期运行样例

## 运行

要求 Node.js ≥ 23.6（原生运行 TypeScript，零依赖；22.6+ 可加
`--experimental-strip-types` 运行）。

```bash
npm test    # node --test test/lsm-tree.test.ts
npm run demo
```

## 验收覆盖

| 验收边界 | 测试 |
| --- | --- |
| 插入 K1=V1 后删除 K1，查询立即 NotFound | `插入 K1=V1 后删除 K1：Point Get 立即返回 NotFound` |
| Tombstone 屏蔽 MemTable/SSTable 中的旧数据 | `Tombstone 屏蔽已落盘 SSTable 中的旧值`、`Range Scan 过滤 Tombstone…` |
| 中间层不得提前丢弃 Tombstone | `中间层 Compaction 保留 Tombstone（更深层仍有更老版本）` |
| 多轮 Compaction 下沉至最后一层后彻底擦除 | `验收场景：多轮 Compaction 后 Tombstone 痕迹被彻底擦除` |
