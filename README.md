# heading-numbering

目录编号库：标题支持移动、改级别、重命名，编号随树结构自动重排；
外部引用绑定稳定 ID，不因编号变化而失效。

## 核心概念

- **稳定 ID**：每个标题有不变的身份标识，编号（如 `1.2.1`）只是其在树中位置的派生显示。
- **增量编号**：每次编辑只从最早受影响的位置重建计数器，并输出编号真正发生变化的 diff（`changes`）。
- **引用解析**：引用在创建时记录编号/标题快照，解析时可对比当前显示与旧版本显示；目标被删除时成为悬空引用并回退到快照显示。
- **禁止跳级**：任何会产生跳级（如 level 1 之后直接出现 level 3）的编辑都会被拒绝并整体回滚，
  库只返回**最小修复范围**建议（`LevelSkipError.fix`），绝不擅自修改正文。

## API 概览

```ts
import { NumberedDocument } from 'heading-numbering';

const doc = new NumberedDocument([
  { id: 'intro', title: '引言', level: 1 },
  { id: 'bg', title: '背景', level: 2 },
]);
```

### 编辑（均返回 `EditResult`）

- `rename(id, title)` — 重命名，不影响编号。
- `move(id, newParentId | null, childIndex?)` — 移动子树到某标题之下（`null` 为顶层），层级自动平移。
- `setLevel(id, newLevel)` — 调整标题连同其子树的绝对层级。
- `remove(id, { cascade? })` — 删除标题；默认子标题上移一级，`cascade: true` 时整棵子树删除。
- `insert(index, { id?, title, level })` / `append(...)` — 插入标题。

成功时返回 `{ ok: true, version, changes, danglingReferences }`；
跳级时返回 `{ ok: false, error: LevelSkipError }`，文档保持原状：

```ts
const result = doc.setLevel('bg', 4);
if (!result.ok && result.error instanceof LevelSkipError) {
  console.log(result.error.fix);
  // { direction: 'lower-following', adjustments: [{ id: 'bg', delta: -2 }] }
}
```

### 引用

```ts
const ref = doc.addReference('bg');      // 记录快照：编号 1.1、标题、版本
doc.move('bg', null);                    // bg 变为第 2 章
const r = doc.resolveReference(ref.id);
// r.status === 'ok'，r.display === '2 背景'，r.snapshotDisplay === '1.1 背景'，r.stale === true

doc.remove('bg');
doc.resolveReference(ref.id).status;     // 'dangling'，display 回退为 '1.1 背景 (已失效)'
doc.danglingReferences();                // 当前全部悬空引用
```

### 同名标题

引用只绑定 ID，同名标题互不干扰。`findByTitle(title)` 返回全部命中；
`getByTitle(title)` 在多命中时抛 `AmbiguousTitleError`。

## 构建与测试

```sh
npm run build
npm test        # 需要 Node >= 18（使用 node:test）
```
