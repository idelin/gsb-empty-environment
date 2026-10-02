# Compact Read-Only Inverted Index Segment

紧凑只读倒排索引段（TypeScript 实现，零依赖）。词典采用前缀压缩字典
（front-coded dictionary），倒排列表存储文档 ID 与词频，文档 ID 按差值
（delta）+ varint 压缩；多词 AND 检索通过跳跃指针（skip pointer）在短链
驱动下快速跳过长链无用段。

要求 Node.js >= 22.6（`--experimental-strip-types`；Node >= 23.6 原生运行
TypeScript）。仅使用 Node 内置模块，测试基于 `node:test`。

## 结构

```
src/bytes.ts       变长 varint / 定长 u32 的字节读写器
src/dictionary.ts  前缀压缩词典：分块 front-coding + 块索引二分查找
src/postings.ts    倒排列表编码（docID 差值 + 词频）与跳跃指针迭代器
src/segment.ts     段构建器（SegmentBuilder）与只读读取器（SegmentReader）
src/intersect.ts   多词 AND 交集求值（短链驱动 + 跳跃指针）
demo.ts            运行样例：构建索引并查询 'database AND storage'
test/              node:test 测试（21 个用例）
```

## 段格式

```
magic      4B   "SIDX"
version    1B
docCount   u32
dictOffset u32         词典起始偏移
-- postings region --  每条倒排链：
  docFreq varint, skipInterval varint, skipCount varint
  跳跃表: { 绝对 docId varint, dataOffset u32 } * skipCount
  dataLength u32
  data: (docDelta varint, tf varint) * docFreq
-- dictionary --       前缀压缩词典：
  块索引（每块首词 + 块偏移），块内首词全存、后续词存 (公共前缀长, 后缀)
  每个词条附带 postingsOffset / postingsLength / docFreq
```

跳跃表每 `skipInterval = max(4, floor(sqrt(docFreq)))` 个 posting 记录一条
（绝对 docId, 字节偏移）。跳跃项保存绝对 docId，因此跳转到边界后无需回读
被跳过的 posting 即可恢复差值解码基准。

## 交集算法

1. 各词倒排链按 docFreq 升序排列，最短链作为驱动链。
2. 取驱动链当前 docId 作为候选，其余链 `advance(candidate)`：
   先在跳跃表中二分定位最后一个 `docId <= target` 的跳跃项，直接重定位
   到边界 posting，再线性扫描至多 `skipInterval` 个 posting。
3. 所有链 docId 相同则产出交集文档；否则以最大 docId 重新对齐驱动链。

## 运行

```bash
node demo.ts        # 构建索引并执行 'database AND storage'
node --test         # 运行全部测试（Node 内置 test runner）
```

（Node 22.6–23.5 需加 `--experimental-strip-types`；也可 `npm run demo` /
`npm test`。）

## 样例输出

语料：50000 篇文档；`storage` 出现在约 5/6 的文档中（docFreq=41760，
长链），`database` 仅出现在 3 个遥远簇共 30 篇文档（短链）。

```
=== compact read-only inverted index segment ===
documents        : 50000
terms            : 15
segment size     : 272839 bytes
posting lists    : "storage" docFreq=41760 (84893 bytes), "database" docFreq=30 (106 bytes)

query: DATABASE AND STORAGE

[skip-pointer intersection]
  matches        : 26 docs
  first matches  : [5000, 5001, 5002, 5003, 5004, 5005, 5006, 5007, ...]
  decoded[database]     : 30 postings, 0 skip jumps
  decoded[storage]      : 251 postings, 3 skip jumps
  total decoded  : 281 postings

[naive linear intersection (baseline)]
  matches        : 26 docs
  decoded[storage]      : 40103 postings, 0 skip jumps
  total decoded  : 40133 postings

results identical: true
long-list decode reduction: 40103 -> 251 postings (99.4% fewer)
```

短链（30 项）驱动下，长链仅 3 次跳跃 + 251 次解码即完成交集，相比朴素
线性扫描少解码 99.4% 的 posting，且结果与朴素算法完全一致。

## 测试结果

```
$ node --test
ℹ tests 21
ℹ pass 21
ℹ fail 0
```

覆盖：varint/u32 编解码边界值、词典查找（命中/未命中/长公共前缀/块边界/
空词典）、倒排链顺序迭代、`advance` 与线性扫描全目标对拍、跳跃指针解码
量上界、混合 next/advance、随机语料上 AND 与朴素集合交集对拍、验收场景
（`database AND storage` 悬殊双链 + 跳跃加速断言）、缺词空结果、坏魔数
拒绝。
