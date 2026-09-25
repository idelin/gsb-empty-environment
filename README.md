# json3-merge

JSON 兼容树的三方合并（three-way merge）：以共同祖先 `base` 为参照，合并两个分支
`ours` / `theirs`。对象递归合并，数组按显式稳定 ID 匹配；无法自动合并的节点记录为
冲突（含三方值与路径），可人工逐项解决。

## 运行环境

- Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译、无第三方依赖）
- 测试使用 Node.js 内置测试运行器 `node --test`

```bash
npm test        # node --test test/*.test.ts
npm run example # node examples/demo.ts
```

## 合并规则

- **对象**：按键递归合并。仅一方修改 → 取修改方；双方修改同一键 → 递归；标量/类型
  不一致 → `modify-modify` 冲突；双方新增同键不同值 → `add-add` 冲突；双方新增同键
  且均为对象 → 递归合并。
- **删除**：双方删除 → 删除；一方删除、另一方未改 → 删除；一方删除、另一方修改 →
  `delete-modify` 冲突（删除侧值记为 `undefined`）。
- **数组**：所有元素必须是携带稳定 ID（默认键 `id`，可用 `idKey` 定制，取值为
  string/number）的对象。按 ID 匹配后逐项三方合并：不同 ID 的项互不影响，可独立
  增删改。顺序规则：共同项的相对顺序若仅一方改变则跟随该方；双方以不同方式重排 →
  `reorder` 冲突（三方值为 ID 序列）；新增项按各自分支中的相对位置插入。
- **输入校验**：合并前校验三份输入，数组项缺 ID（`MISSING_ID`）、ID 类型非法
  （`INVALID_ID`）、ID 重复（`DUPLICATE_ID`）均抛出 `MergeError`，错误携带
  `path`（如 `$.a.list[1]`）与 `side`（`base` / `ours` / `theirs`）。

## 冲突与解决

```ts
interface Conflict {
  path: (string | number)[];        // 冲突节点路径，formatPath 可格式化为 $.users[0].name
  reason: 'modify-modify' | 'delete-modify' | 'add-add' | 'reorder';
  base: JsonValue | undefined;      // 三方值；undefined 表示该侧不存在（已删除/未新增）
  ours: JsonValue | undefined;
  theirs: JsonValue | undefined;
  itemId?: string | number;         // 位于 ID 数组项内时的稳定 ID
}

type Resolution = { take: 'ours' | 'theirs' | 'base' } | { value: JsonValue };
```

- `merge3(base, ours, theirs, options?)` → `{ merged, conflicts }`。`merged` 中冲突处
  默认采用 `ours`（删除侧优先即删除）；传入 `onConflict` 回调可在合并时内联解决。
- `resolveConflict(merged, conflict, resolution)` → 应用单个解决结果（不可变）。
  数组项冲突按 `itemId` 定位，索引漂移不影响正确性。
- `resolveAll(merged, conflicts, resolutions)` → 逐项解决，支持分辨率数组或回调。

## API 一览

| 导出 | 说明 |
| --- | --- |
| `merge3` | 三方合并入口，选项：`idKey`、`onConflict` |
| `resolveConflict` / `resolveAll` | 人工逐项解决冲突 |
| `MergeError` | 输入校验错误，含 `code` / `side` / `path` |
| `formatPath` | 路径格式化（`$.a[0].b`） |
| `deepEqual` / `isPlainObject` | 工具函数 |

## 运行样例

`node examples/demo.ts` 输出（节选）：

```
=== 冲突列表（三方值） ===

$.meta.version  [modify-modify]
  base:   1
  ours:   2
  theirs: 3

$.users[0]  [delete-modify]
  base:   {"id":"u1","name":"Alice","role":"admin"}
  ours:   undefined
  theirs: {"id":"u1","name":"Alice","role":"owner"}

$.settings.pageSize  [modify-modify]
  base:   20
  ours:   50
  theirs: 30

=== 人工逐项解决后的最终结果 ===
{
  "meta": { "version": 3, "title": "Team Board" },
  "users": [
    { "id": "u1", "name": "Alice", "role": "owner" },
    { "id": "u2", "name": "Bob", "role": "member" },
    { "id": "u3", "name": "Carol", "role": "member" }
  ],
  "settings": { "theme": "dark", "pageSize": 100 }
}
```

## 测试结果

`node --test test/*.test.ts`（Node v23.9.0）：24 个测试全部通过。

```
ℹ tests 24
ℹ pass 24
ℹ fail 0
```

覆盖：对象递归合并、modify-modify / delete-modify / add-add / reorder 四类冲突、
双方删除、双方新增对象递归合并、类型变化冲突、数组不同 ID 独立合并、同 ID 项字段级
合并、数组项删除 vs 修改、单方/双方重排、重复 ID / 缺 ID / 非法 ID 拒绝（含深层路径
与来源侧定位）、自定义 `idKey`、`onConflict` 内联解决、`resolveAll` 数组/回调两种
用法、输入不可变性。
