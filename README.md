# lsm-prefix-seek

前缀范围扫描与快速定位器：支持 `PrefixSeek` 的 LSM 索引遍历器。
利用 SSTable 的 Index Block 直接二分定位到第一个可能包含前缀的 Data Block，
零拷贝顺序输出前缀下的所有条目，遇到越界 key 立即截断扫描。

纯 TypeScript 实现，零依赖；需要 Node.js >= 23.6（内置类型擦除，直接运行 `.ts`）。

## 快速开始

```bash
node --test test/*.test.ts   # 运行全部测试（Node 内置 test runner）
node examples/demo.ts        # 运行样例：41 万条多租户记录 + PrefixSeek
```

## 用法

```ts
import { SSTableBuilder, SSTableReader, LSMEngine } from './src/index.ts';

// 写入（key 必须按字典序递增）
const builder = new SSTableBuilder('data.sst', 4096); // 4KB data block
builder.add('user:1001:email', 'a@b.c');
builder.add('user:1001:name', 'alice');
builder.finish();

// 前缀扫描：Index Block 二分定位 -> 顺序流式输出 -> 边界截断
const reader = SSTableReader.open('data.sst');
for (const { key, value } of reader.prefixSeek('user:1001:')) {
  // key/value 均为零拷贝视图，有效期至下一次 next() 调用
}
console.log(reader.stats.dataBlocksRead); // 实际读取的 data block 数

// LSM 引擎：MemTable + 多 SSTable 合并扫描（新数据覆盖旧数据）
const engine = new LSMEngine({ dir: './db', memtableFlushBytes: 4 << 20 });
engine.put('tenant_A:order_000001', '{"amount":42}');
for (const { key, value } of engine.prefixSeek('tenant_A:order_')) { /* ... */ }
```

## SSTable 文件格式

```
[data block 0][data block 1]...[data block N][index block][footer]
```

- **Data block**：entry = `shared(varint) nonShared(varint) valueLen(varint)
  keyDelta value`，key 前缀压缩；每 16 条一个 restart point（存完整 key），
  block 尾部为 restart 偏移数组，支持块内二分查找。
- **Index block**：与 data block 同构；key = 该 data block 的最短分隔符
  （`>=` 块内最大 key 且 `<` 下一块最小 key），value = `(offset, size)`。
- **Footer**：index block 句柄（varint 偏移+长度，填充至 20 字节）+ 8 字节魔数。

## 前缀扫描工作原理

1. **快速定位**：`locateBlock(prefix)` 在内存中的 index entries 上二分查找
   第一个 `separator >= prefix` 的 data block —— 前置不相关 block 零读取。
2. **块内定位**：`BlockIterator.seek` 在 restart point 上二分，再前向解码到
   第一个 `>= prefix` 的 entry。
3. **零拷贝输出**：value 是 SSTable 文件映像的 `subarray` 视图；key 在复用
   缓冲区内重建（视图有效期至下一次 `next()`）。
4. **边界截断**：当前 block 扫完后，先比较其 index separator 与前缀后继
   （`prefixUpperBound`，如 `tenant_A:order_` → `` tenant_A:order` ``），
   若 separator 已越界则直接结束，连下一个 block 都不读；否则读入下一块，
   遇到第一个不带前缀的 key 立即终止。

## 验收结果（test/prefix_seek.test.ts）

410,005 条多租户记录（5 租户 × audit/order/profile/session），3,667 个 data block：

- `PrefixSeek('tenant_A:order_')` 精确输出 60,000 条订单，仅读 618 个 block
  （约为全表的 1/6，即该前缀实际跨越的 block 数），前置 audit block 与
  tenant_B..E 的 block 完全未读；
- 输出严格有序且全部携带前缀，value 共享同一文件映像 ArrayBuffer（零拷贝）；
- 中间租户 `tenant_C:order_` 扫描同样只读其自身 block，验证前置 block 跳过；
- 边界用例：无匹配前缀、表尾前缀、整 key 前缀、超短前缀均正确截断。

## 目录结构

```
src/varint.ts           varint64 编解码
src/block.ts            data block 构建/读取（前缀压缩 + restart point 二分）
src/sstable.ts          SSTable 构建器/读取器、index block 二分定位
src/prefix_iterator.ts  PrefixIterator：前缀扫描 + 边界截断
src/memtable.ts         内存写缓冲
src/engine.ts           LSM 引擎：MemTable + SSTable 合并前缀扫描
test/                   Node 内置 test runner 测试
examples/demo.ts        运行样例
```
