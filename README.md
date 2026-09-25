# json-three-way-merge

对 JSON 兼容树做三方合并（共同祖先 base + 两个分支 ours / theirs）：

- 对象递归合并，键无序（键顺序差异不视为变更）
- 数组有序，按显式稳定 ID（默认 `id` 字段，可用 `idKey` 配置）逐项匹配
- 冲突记录三方值（base / ours / theirs），支持人工逐项解决后重算
- 输入错误（重复 ID、缺 ID、非 JSON 兼容值）抛出 `MergeInputError`，
  携带 `source`（base / ours / theirs）与可定位的 `path`

## 运行要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译、无第三方依赖）。
Node 22.6+ 可加 `--experimental-strip-types` 运行。

```sh
npm test     # node --test "test/**/*.test.ts"
npm run demo # node examples/demo.ts
```

## API

```ts
import { merge3, DELETE, ABSENT, formatPath } from "./src/index.ts";

const result = merge3(base, ours, theirs, {
  idKey: "id",            // 数组项的稳定 ID 字段，默认 "id"
  resolutions: [          // 人工逐项解决：按路径给定最终值
    { path: "$.server.tls", value: DELETE },        // DELETE 表示删除该节点
    { path: ["features", { id: "legacy" }], value: { id: "legacy", enabled: false } },
  ],
});

result.tree;               // 合并树（冲突未解决时暂取 ours，ours 已删则取 theirs）
result.conflicts;          // Conflict[]：{ path, base, ours, theirs }，删除侧为 ABSENT
result.ok;                 // conflicts.length === 0
result.unusedResolutions;  // 未命中任何冲突的 resolution（便于发现拼写错误）
```

## 合并规则

- 两侧相同 → 任取其一；仅一侧相对 base 变更 → 取变更侧。
- 对象：按键集合并递归；键的输出顺序为 base → ours → theirs 的首次出现顺序。
- 数组：按 `idKey` 匹配，同一 ID 的项递归三方合并（字段级）；
  输出顺序为 base 顺序，其后依次追加 ours、theirs 新增的项。
- 一侧删除节点、另一侧修改同节点（含数组项）→ 冲突。
- 双方新增同一 ID 且内容一致 → 正常合并；不一致 → 字段级递归，逐字段冲突。
- 其余双侧都变更且无法递归的情形 → 冲突，记录三方值。

## 路径与错误定位

路径段：对象键为字符串，数组项为 `{ id }`，校验阶段的索引为 `{ index }`。
`formatPath` 输出如 `$.server.tls`、`$.features[id="legacy"]`、`$.items[1]`。
`resolutions` 的 `path` 接受该字符串或等价的段数组。

```ts
try {
  merge3(base, ours, theirs);
} catch (err) {
  // MergeInputError: duplicate id "x" in array (source: ours, path: $.features[id="x"])
  // err.source === "ours"，err.path 为结构化路径段
}
```

## 目录结构

- `src/merge.ts` — 合并核心（校验、递归合并、冲突与解决）
- `src/index.ts` — 公共导出
- `test/merge.test.ts` — Node.js 内置 `node:test` 测试（19 例）
- `examples/demo.ts` — 运行样例：合并 → 冲突展示 → 逐项解决 → 非法输入
