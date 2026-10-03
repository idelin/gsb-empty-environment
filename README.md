# json-crdt-tree

JSON CRDT 层次对象协同解析与变更高阶树（TypeScript 版，零依赖）。

支持 **Array / Map / Register 混合嵌套** 的复合 JSON 文档，多副本离线并发修改后，
通过操作日志交换完成**三向合并**（共同祖先 + 两个并发分支），保证：

- 并发修改**不同层级 / 不同键**的属性精准合并，互不覆盖；
- 合并**幂等、可交换**，所有副本收敛到同一状态（强最终一致性）；
- 合并**就地更新**节点状态，已持有的子对象内存引用（如 `root.list[0]`）不被替换。

## 运行环境

- Node.js **>= 23.6**（原生 TypeScript 类型擦除，无需编译、无任何依赖）
- Node 22.6+ 可加 `--experimental-strip-types` 运行

```bash
npm test    # node --test 'test/*.test.ts'（Node.js 内置测试运行器）
npm run demo
```

## 设计

```
src/crdt.ts   核心状态机（CmRDT）：节点表 + 操作应用，幂等
src/doc.ts    Doc：Proxy 读写 API、操作生成（lamport 时钟）、合并
src/index.ts  公共导出
```

**数据模型** —— 整棵树扁平化为 `nodeId -> Node` 的节点表：

| 类型 | 实现 | 冲突仲裁 |
| --- | --- | --- |
| Map | `key -> ChildValue`，删除用墓碑（记录最大 opId） | 每键 LWW（按 opId 全序） |
| List | RGA：元素带全局唯一 `elemId`，删除为墓碑标记 | 并发同位插入按 `elemId` 降序决胜 |
| Register | 标量 + 最后写入的 opId | LWW |

**操作日志** —— 每次变更生成 `map-set / map-delete / list-insert / list-delete`
操作（携带 lamport 序号 + actor 的全局唯一 id），追加到本地因果有序日志。
`a.mergeFrom(b)` 幂等重放 b 日志中 a 未见的操作；`Doc.merge(a, b)` 双向合并即收敛。
合并时本地 lamport 时钟推进到已见最大序号，保证后续操作在 LWW 仲裁中不会
错误地输给祖先状态。

**引用稳定性** —— Proxy 按 `nodeId` 缓存：合并只就地更新节点内部状态，
`doc.root.list[0]` 在合并前后是同一个 JS 对象。

## 用法

```ts
import { Doc } from './src/index.ts';

const seed = new Doc('seed');
seed.root.list = [{ title: 'draft', tags: ['js'] }];

const alice = seed.fork('alice');
const bob = seed.fork('bob');

// 并发修改同一列表项的不同层级属性
alice.root.list[0].title = 'published';  // Map 内 Register
bob.root.list[0].tags.push('go');        // 嵌套 List

Doc.merge(alice, bob);                   // 三向合并

alice.root.list[0].title;                // 'published'
alice.root.list[0].tags.toArray();       // ['js', 'go']
alice.toJSON();                          // { list: [ { title: 'published', tags: ['js','go'] } ] }
```

列表代理支持 `length`、下标读 / 写（替换）、`push`、`insertAt`、`deleteAt`、
`for...of` 迭代及 `map`/`forEach` 等只读数组方法（委托给快照）。

## 测试

`test/crdt.test.ts`（Node.js 内置 `node:test`，11 个用例）：

- **验收边界**：并发修改 `root.list[0].title` 与 `root.list[0].tags.push('go')`，
  合并后两个属性均正确更新，且列表项内存对象引用未被覆盖；
- 任意深度 Map / List / Register 混合嵌套读写、深层并发修改不同键；
- 合并幂等、可交换（不同合并顺序收敛一致）；
- Register 并发写 LWW 确定性收敛、List 并发同位 push 顺序一致（RGA）；
- Map 删除与并发 set 仲裁、List 删除墓碑、下标替换、`fork` 独立性。
