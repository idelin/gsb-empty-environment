# module-symbol-resolver

多文件模块符号解析库（TypeScript）。只支持三种模块结构：**命名导出**（本地定义）、
**显式导入**、**再导出**（含 `as` 重命名）。模块以数据形式描述（`ModuleSpec`），
`from` 字段直接引用模块 id，路径解析与文件加载不在本库范围内。

## 语义

- 把"模块 M 导出的名字 N"解析为再导出图上的可达性问题：节点是 `(模块, 名字)` 对，
  边是 `export { a as b } from 'T'`。
- **循环导入允许**：导入是叶子引用，不参与导出解析。
- **无定义的循环再导出被拒绝**：可达定义为空且可达子图含环时返回 `circular`
  （附环的见证路径）；环上若能到达定义则正常解析。
- **同名冲突**：可达定义 ≥ 2 时返回 `conflict`，给出**两条最短来源链**
  （按链长、再按字典序取前两条，菱形汇聚到同一定义不算冲突）。
- **无定义且无环**：返回 `unresolved`。

## 增量与快照

```ts
import { Project } from 'module-symbol-resolver';

const project = new Project();
project.setModule('/d.ts', { definitions: ['x'] });
const s1 = project.setModule('/a.ts', { reexports: [{ name: 'x', from: '/d.ts' }] });

s1.resolveExport('/a.ts', 'x');           // resolved -> /d.ts#x

const s2 = project.setModule('/d.ts', {}); // 删除最终定义
s2.resolveExport('/a.ts', 'x');           // unresolved（受影响符号已更新）
s1.resolveExport('/a.ts', 'x');           // 仍为 resolved（旧快照可查）
```

- 每次 `setModule` / `removeModule` 产生新的不可变 `Snapshot`，旧快照永久可查。
- 解析结果按 `(模块, 名字)` 缓存，并记录计算时读取过的模块集合；修改单个模块时
  只失效依赖集合中包含该模块的条目，未受影响的符号直接复用缓存
  （`snapshot.stats` 中的 `computed` / `reused` 可观察）。
- `project.affectedSymbols(id)` 返回已计算符号中依赖 `id` 的条目。
- `snapshot.diagnostics(module)` 汇总该模块的导入/再导出错误与同名冲突。

## 参考验证

`src/reference.ts` 是独立的参考实现：对每次查询**从引用点重新搜索**，
无缓存、无共享状态，且使用与主引擎不同的算法（DFS 可达性 + 逐定义 BFS 最短路 +
逐节点自可达判环）。`validateSnapshot(snapshot)` 对快照中每个模块的每个导出名与
每个显式导入逐一交叉比对两个实现的结果。测试覆盖菱形依赖、重命名链、
删除最终定义，以及 60 个随机种子下的随机模块图与随机增删改序列。

## 运行测试

需要 Node.js ≥ 22.6（原生 TypeScript 类型擦除；源码只使用可擦除语法）：

```sh
node --test test/*.test.ts   # 或 npm test
```

## 结构

- `src/types.ts` — 模块数据模型、解析结果与诊断类型
- `src/resolver.ts` — 主解析引擎（BFS 最短路 + 子图判环），输出依赖集合供增量失效
- `src/project.ts` — `Project` / `Snapshot`：不可变快照、按依赖精确失效的增量缓存
- `src/reference.ts` — 独立参考实现（逐引用点暴力搜索）
- `src/validate.ts` — 主实现与参考实现的交叉验证
- `test/resolver.test.ts` — 确定性场景（菱形、重命名、冲突、环、增量、删定义）
- `test/fuzz.test.ts` — 随机图 + 随机更新序列的交叉验证
