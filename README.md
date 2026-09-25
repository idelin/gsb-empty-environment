# stable-id-tree-patch

基于**稳定节点 ID** 计算两棵 JSON 兼容树之间的最小编辑补丁（插入 / 移动 / 删除 / 属性更新），并可将补丁应用回旧树得到新树。纯 TypeScript，零运行时依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 数据格式

```ts
interface TreeNode {
  id: string;                        // 稳定身份，全树唯一
  attrs?: { [key: string]: JsonValue }; // 无序属性（对象键序无关）
  children?: TreeNode[];             // 有序子节点（数组顺序有意义）
}
```

- 输入必须是 JSON 兼容值：不允许 `undefined`、函数、`NaN`/`Infinity`、循环引用等。
- 校验错误抛出 `TreeError`，其 `path` 字段是 RFC 6901 风格的 JSON 指针，
  可定位到具体来源，例如 `/children/1/id`、`/attrs/bad`；应用补丁时的
  引用错误定位到具体操作，例如 `/ops/0`。

## 补丁操作

| 操作 | 字段 | 含义 |
| --- | --- | --- |
| `insert` | `id, parentId, index, attrs` | 创建节点；`parentId: null` 表示成为根 |
| `update` | `id, set, unset` | 最小属性更新：仅变更/新增键与删除键 |
| `move` | `id, parentId, index` | 跨父移动或同父重排，保留节点身份 |
| `remove` | `id` | 删除整棵子树（只发最顶层被删节点） |

所有 `index` 均指**新树中的最终位置**。补丁按规范顺序输出：
`insert`（先序遍历，**父先于子创建**）→ `update` → `move` → `remove`
（后三类按 id 排序），因此按数组顺序应用时**无悬空引用**。

## 语义保证

- **最少必要**：未变化节点不产生操作；同父重排用 LIS（最长递增子序列）
  求出最少移动次数；删除只发每个被删子树的最顶层节点。
- **身份保留**：跨父移动生成 `move`，而非 `remove` + `insert`。
- **不重复删除**：子节点先移走后再删除其父节点时，子节点不会收到 `remove`。
- **确定性**：输出只取决于输入的语义内容，与 JSON 对象键序无关。

## 运行

需要 Node.js ≥ 22.6（TypeScript 类型擦除；23.6+ 原生支持，可去掉 flag）。

```bash
npm test      # node --experimental-strip-types --test
npm run sample
```

## 运行样例

`examples/sample.ts`：属性变更 + 跨父移动 + 删除父（子已移走）+ 插入子树 + 重排。
输出补丁（节选）：

```json
[
  { "op": "insert", "id": "footer", "parentId": "app", "index": 2, "attrs": { "sticky": true } },
  { "op": "insert", "id": "status", "parentId": "footer", "index": 0, "attrs": {} },
  { "op": "update", "id": "app", "set": { "theme": "dark", "version": 2 }, "unset": [] },
  { "op": "update", "id": "content", "set": { "layout": "list" }, "unset": [] },
  { "op": "update", "id": "menu", "set": { "collapsed": true }, "unset": [] },
  { "op": "move", "id": "archived-report", "parentId": "sidebar", "index": 1 },
  { "op": "move", "id": "content", "parentId": "app", "index": 0 },
  { "op": "remove", "id": "trash" }
]
```

`archived-report` 在 `trash` 被删除前移到 `sidebar` 下，身份保留；
`stale-draft` 随 `trash` 一起删除，不产生额外 `remove`。应用结果与目标树
规范化后完全一致（`round trip matches target: true`）。

## 测试结果

Node v23.9.0，`node --experimental-strip-types --test`：

```
✔ attribute update is minimal: set changed, unset removed, rest untouched
✔ identical trees produce an empty patch
✔ insert of a subtree creates parents before children
✔ cross-parent move preserves identity (move, not remove+insert)
✔ deleting a parent does not re-delete a child that moved out
✔ nested deletes collapse to the topmost remove only
✔ same-parent reorder moves the minimal set (LIS complement)
✔ root replacement and empty-tree edges
✔ patch is deterministic regardless of JSON key order
✔ validation errors carry precise paths
✔ apply errors locate the offending op
✔ applyPatch does not mutate the input tree
✔ round trip: apply(diff(a, b), a) deep-equals b for 300 random cases
ℹ tests 13  ℹ pass 13  ℹ fail 0
```

## 目录结构

- `src/types.ts` — `TreeNode` / `Op` 类型与补丁顺序约定
- `src/validate.ts` — 输入校验，`TreeError` 携带 JSON 指针路径
- `src/diff.ts` — `diffTrees(old, new)` 生成最小确定补丁
- `src/apply.ts` — `applyPatch(old, ops)` 应用补丁（不修改输入）
- `src/util.ts` — JSON 深比较、规范化等
- `test/diff.test.ts` — `node:test` 测试（含 300 例随机往返性质测试）
- `examples/sample.ts` — 可运行样例
