# dynamic-clique-toolkit

动态图极大团枚举与团树分解器（TypeScript，零依赖）。

## 功能

- **极大团枚举**：Bron-Kerbosch 算法 + Tomita 主元剪枝（`src/bronKerbosch.ts`）
- **动态追踪**：增删边时报告极大团的诞生（合并）与消解（退化）（`src/dynamicCliques.ts`）
- **集合查询**：列出包含指定节点集合的全部极大团
- **团树分解**：以极大团为顶点、交集大小为边权，Kruskal 最大生成树构造团树，
  并校验运行交性质（`src/cliqueTree.ts`）

## 环境要求

Node.js ≥ 22.6（原生类型擦除直接运行 `.ts`，无需编译步骤）。

## 使用

```bash
npm test    # node --test（Node.js 内置测试运行器）
npm run demo
```

```ts
import { DynamicCliqueTracker } from './src/dynamicCliques.ts';

const tracker = new DynamicCliqueTracker([[1, 2], [2, 3], [1, 3]]);
const delta = tracker.addEdge(3, 4);
console.log(delta.born, delta.dissolved);
console.log(tracker.cliquesContaining([1, 2]));
```

## 测试覆盖

- 教科书样例图的极大团枚举正确性
- 随机图上与暴力枚举结果对照（30 组）
- 社交网络紧密圈子发现
- 加边促成 5 节点完全子图：新极大团诞生、被包含子团消解
- 删边后 K5 正确退化、继续删边继续分裂
- 团树最大生成树边权与运行交性质
