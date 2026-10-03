# Compact Read-Only Inverted Index Segment (TypeScript)

紧凑只读倒排索引段：前缀压缩词典 + 差值压缩倒排列表 + 跳跃指针交集求值。
纯 TypeScript、零依赖，使用 Node.js 内置 `node:test` 测试。

## 要求

- Node.js ≥ 22.6（`--experimental-strip-types`），推荐 ≥ 23.6（原生运行 TS，可省略该 flag）

## 结构

```
src/bytes.ts       varint(LEB128) 编解码、字节序比较
src/dictionary.ts  前缀压缩（front-coding）有序词典：块首全词 + 块内 (共享前缀长, 后缀)，
                   块偏移表支持二分查找定位块后块内线性扫描（FST 的前缀字典替代方案）
src/postings.ts    倒排列表：(docId 差值 varint, 词频 varint)，每 16 条记录一个跳跃指针
                   （块基 docId + 字节偏移），迭代器 advance() 借助跳跃表快速定位
src/segment.ts     段构建器 SegmentBuilder / 只读段 Segment，多词 AND 交集：
                   按链长升序排序，最短链驱动，其余链用跳跃指针 advance
```

## 段二进制格式

```
[magic "SIS1"][dictLen varint][dictionary block][postings region]
dictionary: termCount, blockCount, blockOffsets*(delta), blocks...
  block: firstTerm(len+bytes), (prefixLen, suffix, offsetDelta, len, docCount)*
posting list: skipCount, (baseDocId, offset)*, (docDelta, freq)*
```

## 运行

```bash
npm test    # node --experimental-strip-types --test test/segment.test.ts
npm run demo
```

## 验收点（test/segment.test.ts）

- 构建倒排索引并检索 `database AND storage`，结果与暴力集合交集一致
- 两词倒排链长度悬殊（database=12，storage=3000）
- 短链驱动交集：长链仅解码 ≤ 命中数 + 12×SKIP_INTERVAL 条 posting，
  且不足链长 1/10，跳跃指针实际生效（skipsUsed > 0）
- 词典前后缀压缩查找、词频保真、跳跃边界精确落点、多词 AND、
  未知词空结果、段文件写读往返

## 演示输出（examples/demo.ts）

storage 链 100,000 条、database 链 10 条时：

```
Query: database AND storage
Result (9 docs): [7, 512, 1024, 5000, 12345, 40000, 77777, 90001, 99999]
  database   chain length      10 | postings decoded    10 | skip jumps 0
  storage    chain length  100000 | postings decoded    90 | skip jumps 8
Skip pointers let the long 'storage' chain skip 99910 of 100000 postings (99.91% never decoded).
```
