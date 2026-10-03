# dynamic-clique-toolkit

动态图极大团枚举与团树分解器（TypeScript，零依赖）。

## 功能

- **极大团枚举**：Bron–Kerbosch 算法 + 主元（pivot）剪枝，见 `src/bronKerbosch.ts`。
- **动态追踪**：`DynamicCliqueTracker` 在增删边时增量维护全部极大团，
  报告每次变化的「诞生 / 消解」清单，并支持查询包含指定节点集合的所有极大团，
  见 `src/tracker.ts`。
- **团树分解**：最大势搜索（MCS）判定弦性；弦图上以极大团为袋、
  按袋交集大小取最大生成树，得到满足运行交性质的团树（联结树），
  见 `src/cliqueTree.ts`。

## 动态更新原理

- **加边 (u,v)**：新诞生的极大团必同时含 u,v；而含 u,v 的团 K 极大 ⟺
  K\{u,v} 是公共邻域 G[N(u)∩N(v)] 的极大团，故只需在公共邻域上跑
  Bron–Kerbosch。旧极大团若只含一个端点且另一端点与其全相邻，则被吸收剔除。
- **删边 (u,v)**：同时含 u,v 的旧极大团失效；可证明删边后新诞生的极大团
  必为退化候选 C\{u}、C\{v} 之一，且仅当不被其他现存极大团包含时成立。

## 环境要求

Node.js ≥ 23.6（原生支持 TypeScript 类型擦除，无需编译）。
Node 22.6+ 可加 `--experimental-strip-types` 运行。

## 运行

```bash
npm test        # Node.js 内置测试运行器（node:test）
npm run example # 社交网络紧密圈子发现样例
```

## 样例输出（examples/social-circle.ts）

5 位用户除 (0,4) 外两两互为好友；加边 (0,4) 促成 5 节点完全子图 K5，
两个旧圈子被消解；删边后正确退化：

```
初始极大团: {0, 1, 2, 3}  {1, 2, 3, 4}
包含用户 1,2 的圈子: {0, 1, 2, 3}  {1, 2, 3, 4}

--- 加边 (0, 4)：0 与 4 成为好友 ---
新诞生的极大团: {0, 1, 2, 3, 4}
被消解的旧团  : {0, 1, 2, 3}  {1, 2, 3, 4}
当前极大团    : {0, 1, 2, 3, 4}
团树分解      : 弦图 = true | 袋 = {0, 1, 2, 3, 4}

--- 删边 (0, 4)：0 与 4 解除好友 ---
新诞生的极大团: {1, 2, 3, 4}  {0, 1, 2, 3}
被消解的旧团  : {0, 1, 2, 3, 4}
当前极大团    : {1, 2, 3, 4}  {0, 1, 2, 3}
```

## 测试结果

`node --test`：13 个测试全部通过，包括：

- Bron–Kerbosch 与暴力子集枚举对拍（随机小图）；
- 验收场景：一条边促成 K5、旧团消解、删边退化；
- 差分测试：300 步随机增删边后与全量重算结果逐步一致；
- 团树：C4 非弦判定、分隔集正确性、运行交性质验证。

## API 速览

```ts
import { DynamicCliqueTracker } from './src/tracker.ts';
import { buildCliqueTree } from './src/cliqueTree.ts';

const tracker = new DynamicCliqueTracker([[0, 1], [1, 2], [0, 2]]);
tracker.addEdge(2, 3);                 // => { born, dissolved }
tracker.maximalCliques();              // 当前全部极大团
tracker.maximalCliquesContaining([0]); // 包含指定节点集合的极大团
tracker.removeEdge(0, 1);
buildCliqueTree(tracker.graph, tracker.maximalCliques()); // 团树分解
```
