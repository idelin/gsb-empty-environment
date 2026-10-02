# rolling-hash-cdc-merkle

基于 **Buzhash 滚动哈希** 的可验证内容定义分块器（Content-Defined Chunking）+ **默克尔树**，
用 TypeScript 实现，使用 Node.js 内置测试运行器（`node:test`）验证。

## 原理

- **Buzhash 滚动哈希**（`src/buzhash.ts`）：48 字节滑动窗口，`h = rotl1(h) ⊕ rotl_w(table[out]) ⊕ table[in]`，
  哈希表由确定性 splitmix32 PRNG 生成，结果可复现、可验证。
- **内容定义分块**（`src/chunker.ts`）：当窗口哈希低 13 位全为 1 时切分（目标平均 8KB），
  最小 2KB / 最大 64KB。边界只取决于局部内容，文件中间插入/删除字节仅影响附近 1~2 个分块。
- **默克尔树**（`src/merkle.ts`）：叶子为分块 SHA-256，内部节点 `sha256(0x01 ‖ left ‖ right)`，
  奇数节点晋升；支持证明生成/验证、叶子 diff、变化路径统计。

## 运行

需要 Node.js ≥ 23.6（原生 TypeScript 类型擦除；Node 22.6+ 可加 `--experimental-strip-types`）。

```bash
npm test     # node --test 'test/*.test.ts'
npm run demo # node examples/demo.ts
```

## 验收结果（10MB 文件中间插入 100 字节）

```
分块数量: 旧 1049, 新 1049
哈希复用率:   1048/1049 = 99.90%
受影响分块:   旧 [513, 514), 新 [513, 514)   ← 仅 1 个分块
需传输的新分块数:     1
需更新的默克尔节点数: 12/2105
增量数据占比:         0.62%
```

增量传输只需：**1 个受影响分块 + 从该叶子到根的默克尔路径更新**（约 12 个节点），
其余 99.9% 分块哈希直接复用。删除字节的场景同样满足局部性（见测试）。

## 测试

```
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

覆盖：Buzhash 滚动正确性（与全量重算逐窗口比对）、确定性、雪崩分布；
分块完整性/大小约束/平均大小；默克尔树证明验证、篡改检测；
10MB 插入/删除的复用率与局部性验收。

## 文件结构

```
src/buzhash.ts   Buzhash 滚动哈希
src/chunker.ts   内容定义分块器
src/merkle.ts    默克尔树 + 证明 + diff
src/util.ts      确定性伪随机数据生成器
test/            node:test 测试
examples/demo.ts 增量同步演示
```
