# suggest-merge

TypeScript 建议（autocomplete）合并库：聚合多个异步来源的候选，按规范键去重，
得分取最大、说明保留来源集合；来源撤回后重新选赢家；高亮项在列表变化时尽量按身份保留。

## 运行要求

- Node.js >= 23.6（原生运行 TypeScript），或 >= 22.6 时加 `--experimental-strip-types`。
- 仅使用可擦除类型语法（`import type`、无 enum/namespace），无需编译步骤。

```sh
npm test   # node --test "test/*.test.ts"
```

## 结构

- `src/merger.ts` — `SuggestionMerger`：同步核心。
  - 规范键：`trim` + 折叠空白 + 小写（可注入自定义 `KeyNormalizer`）。
  - 每个规范键保留 `sourceId -> contribution` 映射；合并项得分取最大，
    `sources` 为全部贡献来源的排序集合，label/detail 来自当前赢家
    （同分时按 sourceId 字典序，保证确定性）。
  - `replaceSource` 整体替换某来源的贡献；`withdrawSource` 撤回后从剩余
    贡献中重新选赢家；最后一个来源撤回时条目消失。
  - 排序：得分降序，label、id 升序兜底（全量聚合排序，与到达顺序无关）。
  - 高亮：`highlight(id)` / `moveHighlight(delta)`；列表变更后若高亮项仍在
    则按身份（规范键）保留并跟踪新位置，否则回退到原索引（ clamp 后）的项。
- `src/engine.ts` — `SuggestionEngine`：异步协调层。
  - `query(text)` 开启新代际（generation），中止上一代的 `AbortSignal` 并清空状态。
  - 来源以任意顺序 resolve；`accept(sourceId, generation, candidates)` 丢弃
    非当前代际的迟到结果。
  - `unregisterSource` / `withdraw` 触发赢家重选与高亮调和。

## 测试覆盖（`test/`）

- 规范键去重（大小写/空白变体合并）、最大得分与来源集合。
- 来源撤回后的赢家重选与条目消失。
- 高亮按身份穿越重排序，消失后按位置回退，边界 clamp。
- 乱序返回的全量聚合排序（手动控制各来源 resolve 顺序）。
- 查询代际切换：过期代际结果被丢弃，中止的 fetch 不产生未捕获异常。
