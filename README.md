# K-Core 分解与剥离器（TypeScript）

面向大型稀疏图的核度（Core-Number）分解与增量维护：

- **全量分解**：Batagelj–Zaversnik 剥离算法，O(V + E)，动态移除度数小于 K 的节点直到收敛；
- **分层输出**：`layers()` 按核度分层，`kCore(k)` 输出核度 ≥ k 的诱导子图；
- **增量维护**：增删边时利用「单条边变化只影响核度 = K（K = min(core(u), core(v))）的节点、且至多变化 1」的性质，
  仅在种子所在的核度-K 连通分量内做局部剥离传播，开销远小于全量重算。

## 环境要求

- Node.js ≥ 22.6（原生 TypeScript 类型擦除，无需编译；开发用的是 v23.9）
- 无第三方依赖；测试使用 Node 内置 `node:test`

## 目录结构

- `src/graph.ts` — 无向稀疏图（邻接集合，O(1) 增删边）
- `src/kcore.ts` — K-核分解、分层输出、增量核度传播
- `examples/acceptance-graph.ts` — 验收图（密集子网 + 缺边 gadget + 悬挂链）
- `examples/demo.ts` — 运行样例
- `test/kcore.test.ts` — Node 内置测试

## 运行

```bash
npm test      # 运行测试
npm run demo  # 运行验收样例
```

## 运行样例（`npm run demo`）

验收图构成：K5 稠密团（核度 4）、K4 稠密团（核度 3）、K4 缺一条边的 gadget（核度 2）、悬挂链（核度 1）。

```text
=== 验收图：密集子网 (K5, K4) + 缺边 gadget + 悬挂链 ===
节点数=16, 全量分解开销: vertices=16, edgeRelaxations=50

--- 核度分层（Core-Number -> 节点） ---
core=1: [13, 14, 15]
core=2: [9, 10, 11, 12]
core=3: [5, 6, 7, 8]
core=4: [0, 1, 2, 3, 4]

--- 1-core 到 4-core 分层子图 ---
1-core: |V|=16, |E|=25, nodes=[0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15]
2-core: |V|=13, |E|=22, nodes=[0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]
3-core: |V|=9, |E|=17, nodes=[0, 1, 2, 3, 4, 5, 6, 7, 8]
4-core: |V|=5, |E|=10, nodes=[0, 1, 2, 3, 4]

=== 增量加边：桥接边 (9,10)，使缺边 gadget 成为 K4 ===
更新前 gadget 核度: [2, 2, 2, 2]
更新后 gadget 核度: [3, 3, 3, 3]
局部更新: visited=4, changed=4, edgeRelaxations=0
对照全量重算需访问 vertices=16 （局部开销仅为其 25.0%）

=== 增量删边：删除 K5 内边 (0,1) ===
K5 节点核度: [3, 3, 3, 3, 3]
局部更新: visited=5, changed=5, edgeRelaxations=9

增量维护结果与全量重算一致: YES
```

## 测试结果（`npm test`）

```text
✔ 全量分解：密集子网 + 悬挂链的核度正确
✔ 分层输出：1-core 到 4-core 子图逐层嵌套且满足最小度约束
✔ 增量加边：桥接边 (9,10) 使局部核度 2 -> 3，开销远小于全量重算
✔ 增量删边：删除 K5 内边使局部核度 4 -> 3，且与全量重算一致
✔ 边界：重复边、不存在的边、新节点均为幂等/安全操作
✔ 差分测试：随机增删边后，增量维护结果始终等于全量重算
ℹ 有效操作 393 次，平均局部区域 31.19 / 全图 60 节点
ℹ tests 6 / pass 6 / fail 0
```

差分测试在 60 节点模块化稀疏图上执行约 400 次随机增删边，
每次增量更新后都与全量重算逐一比对核度，结果完全一致。

## 算法说明

**全量分解**：桶排序按残余度数升序剥离，节点被剥离时的残余度数即为其 Core-Number。

**增量维护**（`addEdge` / `removeEdge`）：

1. 令 K = min(core(u), core(v))（更新前核度）。理论保证：只有核度恰为 K 的节点可能变化，且至多变化 1；
2. 圈定局部区域 R = u/v 所在的「核度 = K」连通分量，区域外节点核度不变；
3. 区域内以阈值做局部剥离：核度 > K 的外部邻居视为固定支撑，
   加边时存活者升为 K+1，删边时被剥离者降为 K−1；
4. 返回 `UpdateStats`（visited / changed / edgeRelaxations）用于量化局部开销。

## 类型检查（可选）

仓库零依赖、直接以 Node 运行 `.ts`。如需静态类型检查：

```bash
npm i -D typescript @types/node && npx tsc --noEmit
```
