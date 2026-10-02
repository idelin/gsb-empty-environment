# LSEQ — 基于无界分数因果树的高效协同序列 (TypeScript)

LSEQ 序列 CRDT：每个元素由一棵**无界因果树**上的路径标识符 `(digit, site)[]`
寻址，标识符全序即序列的因果序。合并任意乱序的操作流后，所有副本收敛到同一序列。

## 自适应可变基数空间分配

- **可变基数**：树的分支因子随深度翻倍（8, 16, 32, 64, …），浅层紧凑、深层宽敞，
  避免标识符位宽爆炸。
- **方向自适应 boundary 策略**：前插（开放下界）用 `boundary-` 贴近上界分配，
  后插（开放上界）用 `boundary+` 贴近下界分配，中间插入随机选择；
  为未来密集编辑保留最大剩余空间。
- **按需下沉**：仅当当前层区间耗尽时才携带前驱层级下沉一层，
  深度增长为编辑次数的多项式对数（1000 次密集首尾交替插入 → 最大深度 9 ≈ log₂1000）。

## 运行

需要 Node.js ≥ 22.6（使用内置 type stripping 直接运行 TypeScript，无需编译）。

```bash
npm test     # Node.js 内置测试运行器 (node:test)
npm run demo # 运行样例
```

## 样例输出

```text
[dense alternating inserts x1000]
  size=1000  maxDepth=9  meanDepth=7.48
  (log2(1000) ~= 9.97 -> depth growth is poly-logarithmic, not linear)

[3 replicas + late joiner, random-order merge]
  merged=14 ops  sequence="amegatmaahaplb"
  identifiers (depths): 2,2,1,1,1,1,1,1,1,2,3,2,2,1
```

## 测试结果

```text
ok 1 - compareIdentifiers: total order with site tiebreak and prefix rule
ok 2 - allocate: identifier always lands strictly between neighbours
ok 3 - variable base doubles with depth
ok 4 - acceptance: 1000 dense alternating front/back inserts, gentle depth growth
ok 5 - acceptance: multi-replica random-permutation merge preserves strict causal order
ok 6 - concurrent inserts at the same position converge across replicas
# tests 6  # pass 6  # fail 0
```

## 结构

- `src/lseq.ts` — 标识符分配（`allocate`）、全序比较（`compareIdentifiers`）、
  因果树序列（`LSEQSequence`：本地插入 / 幂等合并 / 深度统计）。
- `test/lseq.test.ts` — 单元测试 + 两条验收边界测试。
- `examples/demo.ts` — 密集交替插入与多副本随机合并样例。
