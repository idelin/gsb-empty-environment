# rolling-hash-cdc

可验证的内容定义分块（Content-Defined Chunking）+ 默克尔树，用于大文件增量同步。
纯 TypeScript，零依赖，使用 Node.js 内置 `node:test` 测试。

## 原理

- **Buzhash 滚动哈希**（`src/buzhash.ts`）：64 字节滑动窗口上的 O(1) 滚动哈希，
  哈希表由固定种子的 splitmix32 确定性生成，任何一方可独立复现验证。
- **内容定义分块**（`src/chunker.ts`）：当滚动哈希低 13 位全为 1 时切分
  （平均 8 KB，最小 2 KB，最大 64 KB）。切点只取决于局部字节内容，与绝对偏移无关，
  因此中间插入字节只会局部影响 1–2 个分块，后续边界自动重新对齐。
- **默克尔树**（`src/merkle.ts`）：叶子为各分块的 SHA-256，内部节点为
  `sha256(left || right)`，奇数节点直接晋升。支持证明生成与验证。
- **增量同步**（`src/sync.ts`）：按分块哈希做内容寻址 diff，
  增量传输 = 变化的分块 payload + 其默克尔认证路径（O(log n)）。

## 环境要求

Node.js ≥ 23.6（原生运行 TypeScript，无需编译、无第三方依赖）。

## 运行

```bash
npm test     # node --test "test/*.test.ts"
npm run demo # node examples/demo.ts
```

## 验收结果（10 MB 文件中间插入 100 字节）

`npm run demo` 实测输出：

```
old chunks:           1,021 (avg 10270 B, chunked in 118 ms)
new chunks:           1,021 (re-chunked in 112 ms)
reused chunks:        1,020 (99.90%)
changed chunks:       1  -> chunk #523 @ offset 5,241,344, 3,679 bytes
tree depth:           11 levels, proof length/change: 10 hashes
total transfer:       4,031 bytes of 10,485,860 (0.0384%)
```

- 分块哈希复用率 **99.90%**（要求 ≥ 99%）
- 受影响分块 **1 个**（要求 1–2 个）
- 增量传输仅 **约 4 KB**（1 个分块 payload + 10 个哈希的默克尔路径 + 新根）

## 测试

`npm test`：13 个用例全部通过，覆盖

- Buzhash 滚动值与全量重算逐字节一致、确定性、reset 行为
- 分块完整覆盖输入、min/max 约束、确定性、边界内容定义性（前缀平移后内部边界 95%+ 复用）
- 默克尔树证明验证、篡改检测、奇数叶子晋升、单叶树
- 验收场景：10 MB 中间插入 100 字节 → 变化分块 ≤ 2 且复用率 ≥ 99%，
  同步计划只含变化分块与 O(log n) 默克尔路径
