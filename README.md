# Vectorized String Prefix & Regex Matcher

批量字符串列过滤算子（TypeScript，零依赖）。字符串列以**紧凑字节数组 + 偏移数组**
列式存储，匹配算子直接在原始字节上批量比对，结果输出为**压缩位图**（1 bit/行），
全程不产生逐行字符串对象解包。

## 架构

- `src/string-column.ts` — `StringColumn`：所有行的 UTF-8 字节拼接在一块
  `Uint8Array` 中，行边界由 `Uint32Array` 偏移数组寻址；`StringColumnBuilder`
  负责构建。
- `src/bitmap.ts` — `Bitmap`：32 位字压缩位图；按字跳过全零块迭代命中行，
  Kernighan popcount 统计基数。
- `src/operators.ts` — 向量化算子：
  - `startsWith(column, prefix)`：在紧凑字节数组上逐行比对前置字节，不解码字符串；
  - `equals(column, value)`：整值字节相等比较；
  - `like(column, pattern)`：SQL `LIKE`（`%` / `_`）。纯前缀模式（如
    `'https://api.%'`）与无通配模式走字节比较快路径，其余编译为锚定正则；
  - `matchRegex(column, regex)`：通用正则路径，逐行惰性解码。

## 运行

需要 Node.js ≥ 22.6（使用内置类型擦除运行 TypeScript，无需编译步骤）。

```bash
npm test     # node:test 内置测试
npm run demo # 10 万行 URL 样例
```

## 样例输出（`npm run demo`）

```
Building column with 100,000 URL rows...
  compact buffer: 2,962,723 bytes, offsets: 400,004 bytes, built in 63.5 ms

Filter: url LIKE 'https://api.%'
  matched 9,812 / 100,000 rows in 3.34 ms
  bitmap: 12,500 bytes (1 bit/row, 8x smaller than a boolean array)

First 5 matching rows (decoded only for display):
  [17] https://api.data.dev/v2/orders
  [32] https://api.example.com/items/42
  ...

Cross-checks:
  startsWith('https://api.')      -> 9,812 rows in 1.77 ms
  matchRegex(/^https:\/\/api\./) -> 9,812 rows in 12.68 ms (decode-per-row path)
```

## 测试结果（`npm test`）

10 个用例全部通过，含验收用例：10 万行 URL 列执行 `url LIKE 'https://api.%'`，
与朴素 JS 语义逐行对拍一致，位图大小恰为 `ceil(100000/32)*4 = 12500` 字节，
单次过滤约 2–4 ms。

```
# tests 10
# pass 10
# fail 0
```
