# 向量化字符串前缀与正则匹配器

批量字符串列过滤算子：字符串列以**紧凑字节数组 + 偏移数组**存储，
前缀（StartsWith）/ LIKE / 正则匹配在底层字节上批量执行，
匹配结果输出为**压缩位图**（每行 1 bit），全程避免频繁字符串对象解包。

## 结构

```
src/column.ts     紧凑字符串列：Uint8Array 字节池 + Uint32Array 偏移数组
src/bitmap.ts     压缩位图：32 位字存储、向量化 and/or/andNot、popcount、RLE 游程压缩
src/operators.ts  向量化算子：startsWith / endsWith / contains / equals / like / matchesRegex
examples/demo.ts  验收样例：10 万行 URL 列执行 url LIKE 'https://api.%'
test/*.test.ts    Node.js 内置测试（node:test + node:assert）
```

## 设计要点

- **紧凑列存储**：所有行的 UTF-8 字节连续存放，`offsets[i..i+1]` 切分第 i 行；
  算子直接在字节池上扫描，不为每行创建 JS 字符串对象。
- **LIKE 编译快路径**：`compileLike` 把 SQL LIKE 模式编译为执行计划——
  `'https://api.%'` → 纯前缀字节比对；`'%.png'` → 后缀；`'%foo%'` → 子串；
  无通配 → 等值；含 `_` 或交错通配才退化为逐行正则（共享 TextDecoder，即时判定不驻留）。
- **压缩位图输出**：命中结果按 32 位字紧凑存储（较 boolean 数组省 32 倍内存），
  支持按字向量化位运算组合多个过滤条件，并可 RLE 游程压缩用于存储/传输。

## 运行

需要 Node.js ≥ 22.6（`--experimental-strip-types` 直接运行 TypeScript，仅使用可擦除类型语法）。

```bash
npm test     # node --experimental-strip-types --test test/*.test.ts
npm run demo # node --experimental-strip-types examples/demo.ts
```

## 实测结果（Node v22.14.0）

测试：20 个用例全部通过（列存储 / 位图 / 各算子 / 10 万行验收）。

```
构建列（100,000 行）: 40.71 ms
列存储: 4,514,490 字节数据 + 400,004 字节偏移（100,000 行）
like(column, 'https://api.%'): 3.80 ms
命中行数: 20,000 / 100,000
原生 filter 命中: 20,000（一致性: OK）
位图: 3,125 字（12,500 字节，每行 1 bit）
matchesRegex /^https:\/\/api\.[^/]+\/v2\//: 14.64 ms，命中 6,667
startsWith 'https://': 1.66 ms，命中 80,000
```

10 万行 URL 列上 `url LIKE 'https://api.%'` 的向量化前缀过滤约 **4 ms**，
结果与原生逐行 `String.prototype.startsWith` 过滤完全一致。
