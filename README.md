# outline-numbering

目录编号库：支持标题移动、改级别、重命名，编号随树结构变化，外部引用始终指向稳定 ID。

## 模型

文档是**扁平标题列表**（`{ id, title, level }`，按文档顺序），树结构由级别推导。
`id` 是稳定标识，重命名/移动/改级别都不会改变它；外部引用只存 `id`。

## 核心能力

- **增量编号**：`applyEdit` 返回 `changes`，只包含本次编辑中编号真正变化的标题
  （`added` / `removed` / `changed`），不重发整个目录。
- **引用解析**：`addReference` / `resolveReference` 按稳定 ID 解析到当前编号与标题；
  目标被删除时返回 `dangling` 及最后已知快照（`lastKnown`）。
- **旧版本引用显示**：引用可钉住 `pinnedVersion`，解析结果同时给出钉住版本与当前版本的
  编号/标题；`formatReferenceDisplay` 输出如 `§1.1 背景（v1，当前为 §2.1 背景介绍）`。
- **禁止跳级**：级别超过结构父级 +1 即为违规。库**绝不擅自修改正文**：
  - `report`（默认）：编辑生效，违规标题编号为 `null`，`violations` 给出**最小修复范围**
    （`fixRange` = 违规标题的整个子树，`suggestion` = 子树整体平移 `delta`，
    `alternative` = 需补插的祖先层级数）；
  - `reject`：抛出 `LevelJumpError`（携带 `violations`），文档状态不变。
- **版本历史**：每次编辑生成快照，悬空引用可回退到最后出现的版本。

## API 摘要

```ts
import { OutlineStore, formatReferenceDisplay } from './src/index.ts';

const store = new OutlineStore([
  { id: 'c1', title: '第一章', level: 1 },
  { id: 's1', title: '背景', level: 2 },
]);

store.addReference({ id: 'ref-1', targetId: 's1', pinnedVersion: 1 });
const result = store.applyEdit({ kind: 'move', id: 's1', to: { afterId: 'c2' } });
// result.changes            —— 增量编号变化
// result.violations         —— 跳级违规（含最小修复范围）
// result.danglingReferences —— 本次编辑产生的悬空引用

const resolved = store.resolveReference('ref-1');
console.log(formatReferenceDisplay(resolved));
```

编辑类型：`insert` / `remove`（`withSubtree` 可选）/ `rename` / `changeLevel` / `move`
（`move` 携带整个子树；`beforeId`/`afterId` 定位）。

## 运行测试

需要 Node ≥ 22.6（原生 TypeScript 类型擦除），无第三方依赖：

```sh
node --test test/*.test.ts
```

## 目录结构

- `src/types.ts` — 公共类型（标题、编号变化、违规、引用解析结果）
- `src/numbering.ts` — 编号计算与跳级检测（最小修复范围）
- `src/store.ts` — `OutlineStore`：编辑、版本快照、增量 diff、引用解析
- `src/display.ts` — 引用显示格式化（含旧版本展示）
- `test/` — 覆盖删除父标题、同名标题、跨章节移动、旧版本引用显示等场景
