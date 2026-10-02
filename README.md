# incremental-scc

动态有向图的**增量强连通分量（SCC）维护器**。TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 特性

- 顶点动态增删：`addVertex` / `removeVertex`
- 有向边插入 / 删除：`addEdge` / `removeEdge`
- 每次变更返回分量级**差量**（`merge` / `split` / `vertex-added` / `vertex-removed`）
- 分量标识 = 分量内**最小节点 ID**，成员不变则标识稳定
- 增量缩点 + 局部遍历，不做全图重算：
  - 边插入：从终点局部前向 DFS、从起点局部反向 DFS，交集即环上顶点（合并集）
  - 边删除 / 顶点删除：仅在受影响分量的诱导子图上做局部 Tarjan（精确分裂）

## 环境要求

Node.js >= 23.6（原生运行 TypeScript，无需编译）。Node 22.6+ 可加
`--experimental-strip-types` 运行。

## 使用

```ts
import { IncrementalSCC } from './src/incremental-scc.ts';

const g = new IncrementalSCC();
g.addVertex(1); // A
g.addVertex(2); // B
g.addVertex(3); // C
g.addEdge(1, 2);
g.addEdge(2, 3);
g.addEdge(3, 1);
// => [{ kind: 'merge', component: { id: 1, members: [1, 2, 3] }, absorbed: [2, 3] }]

g.removeEdge(2, 3);
// => [{ kind: 'split', from: 1, components: [{id:1,...}, {id:2,...}, {id:3,...}] }]
```

## 运行

```bash
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 测试

`test/incremental-scc.test.ts` 共 9 个用例，包括：

- 验收场景：三角环合并 → 挂接独立节点 D → 删边后精准分裂为三个单节点分量，D 不受影响
- 多分量合并、最小 ID 标识稳定性
- 部分分裂（环中套环）、冗余边删除不分裂
- 顶点删除引发分裂、单点分量移除
- 重复边 / 自环 / 缺失边的空操作与报错
- 500 步随机增删边与暴力全量 Tarjan 对拍
