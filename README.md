# lsm-prefix-seek

前缀范围扫描与快速定位器：支持前缀 `Seek` 的 LSM 索引遍历器（TypeScript，零依赖，
仅使用 Node.js 内置模块与内置测试运行器 `node:test`）。

## 设计

```
SSTable 文件布局
┌─────────────┬─────────────┬─────┬─────────────┬────────────┬─────────┐
│ data block0 │ data block1 │ ... │ data blockN │ index block│ footer  │
└─────────────┴─────────────┴─────┴─────────────┴────────────┴─────────┘
                                                          footer(24B) = indexOffset(u64) | indexSize(u64) | magic(8B)

Block 布局（data block 与 index block 共用格式）
  entry*   : varint keyLen | key | varint valueLen | value
  offsets  : uint32LE[numEntries]     -- 每个 entry 的起始偏移（重启点数组）
  trailer  : uint32LE numEntries
```

- **Index Block 直接定位**：index block 的 key 是相邻 data block 间的最短分隔键
  （`shortestSeparator`），value 是 `(blockOffset, blockSize)`。`Seek(prefix)` 先在
  内存中的 index block 上二分，直接命中第一个可能包含前缀的 data block——
  前置不相关 Block 零读取。
- **块内二分**：data block 内每个 entry 的偏移都记录在重启点数组中，块内再次二分
  定位到第一个 `>= prefix` 的 key。
- **零拷贝顺序输出**：key 不做前缀压缩，迭代器返回的 key/value 全部是
  `Buffer.subarray` 视图，直接指向块缓冲，无任何逐条拷贝。
- **边界截断**：`PrefixIterator` 在顺序扫描中一旦遇到不属于前缀的 key
  （如下一个租户），`valid()` 立即为 false，扫描即刻结束，不再触碰后续 Block。

## 目录结构

- `src/varint.ts` — varint 编解码（算术实现，支持 >4GB 文件偏移）
- `src/block.ts` — Block 构建/读取 + 块内二分 `seek`
- `src/sstable.ts` — SSTable 写入器（data+index+footer）与读取器（索引常驻内存）
- `src/iterator.ts` — `SstableIterator` / `PrefixIterator` / `MemTableIterator` / `MergeIterator`
- `src/engine.ts` — 迷你 LSM 引擎：memtable + 多层 SSTable 归并，`prefixSeek(prefix)`
- `test/` — Node.js 内置测试（`node:test`）
- `examples/demo.ts` — 运行样例

## 运行

要求 Node.js ≥ 22.6（推荐 ≥ 23.6，TypeScript 类型擦除开箱即用；仓库 `.nvmrc` 为 v23.9.0）。

```bash
npm test    # node --test test/*.test.ts
npm run demo
```

## 运行样例（`npm run demo`）

```
== building LSM store in /var/folders/.../lsm-demo-XXXX ==
flushed 300,010 records -> 11,539 data blocks, 45.9 MiB in 18060ms

== PrefixSeek('tenant_A:order_') ==
entries returned : 60,000
first key        : tenant_A:order_000000
last key         : tenant_A:order_059999
scan time        : 536.6ms
blocks read      : 2308 of 11539 (20.0% of the table)
bytes read       : 9.1 MiB
value checksum   : 7,860,000 bytes

== PrefixSeek('tenant_D:order_') (middle of keyspace) ==
entries returned : 60,000 (tenant_D:order_000000 .. tenant_D:order_059999)
scan time        : 369.2ms
blocks read      : 2308 of 11539 -- tenants A..C skipped via index binary search, scan stops at tenant_E

== boundary truncation: PrefixSeek('tenant_A:profile') ==
tenant_A:profile = profile-of-tenant_A
blocks read      : 1 (single block, stopped before tenant_A:session*)

== PrefixSeek('user:1001:') on a small user store ==
user:1001:email = user1001@example.com
user:1001:name = name-1001
user:1001:tags:vip = 1
```

## 测试结果（`npm test`，12/12 通过）

```
✔ block roundtrip: keys and values readable in order
✔ block binary search: exact hits, gaps, and out-of-range targets
✔ block keys/values are zero-copy views into the block buffer
✔ prefix seek merges memtable and multiple SSTables, newest wins
✔ prefix seek sees unflushed memtable data only via the memtable source
    scanned 60000 keys in 1984.5ms, blocks read 1936/9678
✔ PrefixSeek streams exactly the tenant prefix and truncates at the boundary
✔ PrefixSeek on user:1001: returns only that user's sub-attributes
✔ PrefixSeek edge cases: missing prefix, prefix past the end, empty match
✔ shortest separator shortens at the first differing byte
✔ iterator scans the whole table in sorted order
✔ seek lands exactly on the target block and reads only blocks from there on
✔ seek only reads the blocks overlapping the scanned range

ℹ tests 12   ℹ pass 12   ℹ fail 0
```

验收边界覆盖（`test/prefix_seek.test.ts`）：

- 5 租户 × 60,000 订单 = **30 万条**多租户记录落盘为单个 SSTable（近万个 data block）；
- `PrefixSeek('tenant_A:order_')` 恰好返回该租户 60,000 条订单，首 key 为
  `tenant_A:order_000000`，遇 `tenant_A:profile`/`tenant_B:*` 边界立即截断；
- 仅读取约 1,936/9,678 个块（≈20%，即该租户数据所占块数 +1 个边界块），
  前置与后续不相关 Block 全部跳过；对中间租户 `tenant_D` 同样成立；
- 60,000 个返回 key 只对应 ~1,936 个底层 ArrayBuffer（每块一个），验证零拷贝。
