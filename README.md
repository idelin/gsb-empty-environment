# LSEQ 协同序列（TypeScript 版）

基于**无界分数因果树**的高效协同编辑序列（LSEQ, Nédelec et al. 2013）。
在两个紧邻字符位置之间生成致密的有理数/二进制标识符，通过**自适应可变基数
空间分配**避免长文本连续输入引起的标识符位宽爆炸。

## 核心设计

- **标识符结构**：路径分量数组 `{ digit, site }[]`，第 `d` 层 digit 取值
  `[0, 2^(4+d))` —— 基数随深度自适应翻倍（16, 32, 64, ...）。
- **boundary± 策略**：每层每站点随机持久选择 boundary+ / boundary-，
  配合**恒定步长**（`BOUNDARY = 8`）。基数翻倍 + 步长恒定使每层可容纳
  元素数随深度指数增长，单向密集插入的深度期望为 **O(log n)**。
- **确定性下探**：终态 digit 恒 ≥ 1；digit 0 仅作"下探标记"。任意
  `prev < next` 之间总能分配出严格居中的标识符（含紧邻边界情形）。
- **CRDT 收敛**：插入/删除操作幂等、可乱序送达；删除用墓碑，
  delete 先于 insert 到达也能收敛。合并结果只依赖操作集合 ⇒ 严格因果序。

## 文件结构

- `src/lseq.ts` — LSEQ 标识符分配、比较、位宽统计
- `src/document.ts` — CRDT 序列文档（insert/delete/merge，墓碑，幂等）
- `src/demo.ts` — 运行样例
- `test/lseq.test.ts` — Node.js 内置测试（`node:test`）

## 运行

需要 Node.js ≥ 22.6（原生运行 TypeScript + `node:test`）。

```bash
npm test    # node --test test/*.test.ts
npm run demo
```

## 运行样例（`npm run demo`）

```
=== 样例 1：开头/结尾 1000 次密集交替插入 ===
  插入  100 次 -> maxDepth=6 avgDepth=4.01 maxBits=231
  插入  200 次 -> maxDepth=7 avgDepth=4.90 maxBits=273
  插入  300 次 -> maxDepth=7 avgDepth=5.43 maxBits=273
  插入  400 次 -> maxDepth=9 avgDepth=6.04 maxBits=360
  插入  500 次 -> maxDepth=9 avgDepth=6.43 maxBits=360
  插入  600 次 -> maxDepth=9 avgDepth=6.69 maxBits=360
  插入  700 次 -> maxDepth=9 avgDepth=6.93 maxBits=360
  插入  800 次 -> maxDepth=9 avgDepth=7.12 maxBits=360
  插入  900 次 -> maxDepth=9 avgDepth=7.28 maxBits=360
  插入 1000 次 -> maxDepth=9 avgDepth=7.40 maxBits=360
  最终: 文本长度=1000 maxDepth=9 maxBits=360

=== 样例 2：多节点并发编辑 + 随机全排列合并 ===
  4 个节点共产生 110 个操作，因果广播后文本长度=282
  随机排列合并 trial=0: 收敛一致 ✓
  ...（trial=1..4 同样收敛）
  结论: 所有随机排列均收敛到严格因果序 ✓
```

## 测试结果（`npm test`，9/9 通过）

```
✔ allocate 生成的标识符严格位于 prev 与 next 之间
✔ 紧邻边界（digit 0 下探标记）之间仍可分配
✔ 并发站点在同一间隙插入产生唯一且有序的标识符
✔ 验收：开头/结尾 1000 次密集交替插入，深度增长平缓
    [密集交替插入] N=1000 maxDepth=9 avgDepth=7.40 maxBits=360
✔ 深度增长曲线：规模扩大 10 倍，深度仅缓慢增加
    [深度曲线] n=100/1000/10000 -> maxDepth=5/9/12
✔ 验收：多节点随机全排列合并后保持严格因果序
    [随机排列合并] sites=5 ops=257 finalLength=825 全部收敛
✔ 操作幂等：重复送达不改变结果
✔ delete 先于 insert 到达（墓碑）仍收敛
✔ bound 随深度指数翻倍（自适应可变基数）
ℹ pass 9 / fail 0
```

## 验收边界说明

- **深度平缓**：1000 次开头/结尾密集交替插入后 `maxDepth=9`（远小于线性
  的 O(n)）；规模 100→1000→10000 时深度 5→9→12，呈对数增长。
- **位宽不爆炸**：最大标识符位宽 360 bit（含 32 bit 站点号/层），
  不随文本长度线性膨胀。
- **严格因果序**：5 节点并发产生 257 个操作，8 组随机全排列合并 +
  逐操作乱序送达，全部收敛到与因果广播一致的唯一序列。
