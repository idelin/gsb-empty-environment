# accname-engine

在无浏览器布局引擎的前提下，对夹具（fixture）给出的节点树计算可访问名称
（accessible name）与描述（description），并在 DOM 式变更后做增量重算、
输出变化通知。TypeScript 编写，Node.js 内置测试运行器（`node:test`）验证，
Node 23 直接以类型擦除方式执行 `.ts`，无需编译步骤。

## 结构

- `src/types.ts` — 节点树、字形度量、依赖键等类型。
- `src/compute.ts` — 纯函数全量计算（参考实现）：
  - 引用优先级：`aria-labelledby` → `aria-label` → 原生标签
    （`role: label` + `for`，`img` 的 `alt`）→ 内容派生名称；
  - 描述：`aria-describedby` → `aria-description`；
  - 隐藏节点在普通内容遍历中被跳过，但被显式引用时参与计算；
  - `presentation` / `none` / `generic` 角色禁止命名；
  - 引用链用 visited 集合做循环检测，跨节点链与环均按规定终止；
  - 嵌套控件（`img`/`textbox` 等）在内容遍历中贡献自身替代文本
    （`alt`/`value`）；
  - 文本是否渲染由夹具字形度量（`charWidth` / 每节点 `overrides`）判定，
    零宽字形视为未渲染；
  - 重复 ID 按文档序取首个出现；
  - 计算过程把读取到的节点属性/文本记录为依赖键（如 `L:text`、
    `n1:attr:aria-labelledby`、`@id:L`、`children:root`）。
- `src/engine.ts` — 增量引擎 `AccNameEngine`：
  - 缓存条目带树版本号，配合每次变更的依赖键历史做失效判定，
    未受影响的条目按版本重新校验而非重算；
  - 反向依赖索引定位受影响节点，变更后只重算这些节点，
    名称/描述发生变化时向 `onChange` 监听器发出通知；
  - 缓存与依赖图均为实例字段，错误引用不会串到其他引擎实例；
  - DOM 式变更 API：`setText` / `setAttribute` / `removeAttribute` /
    `setRole` / `setHidden` / `appendChild` / `removeNode`。

## 运行测试

```sh
npm test   # node --test "test/*.test.ts"，需 Node >= 23（类型擦除）
```

## 测试覆盖

- `test/labelledby-chain.test.ts` — 跨多节点的 `aria-labelledby` 链、
  自引用环、双节点互引环、描述链环，均按规定终止。
- `test/hidden-and-refs.test.ts` — 隐藏但被显式引用 vs 普通隐藏内容；
  多处引用的标签被修改时全部相关节点收到通知，无关文本不触发；
  零宽字形；依赖记录断言。
- `test/mutations.test.ts` — 重复 ID、节点移除、角色改变、嵌套控件替代
  文本，每步变更后与独立全量参考（`fullCompute`）核验。
- `test/cache-isolation.test.ts` — 错误引用实例间隔离、断裂引用被"治愈"
  后仅本实例重算、缓存按版本历史重新校验。

## 实际运行结果（2026-09-26，Node v23.9.0）

```
✔ aria-labelledby chain across multiple nodes terminates on cycle
✔ self-referencing aria-labelledby terminates and falls through to content
✔ mutual cycle between two referenced nodes terminates
✔ aria-describedby chain with cycle terminates
✔ hidden node explicitly referenced by aria-labelledby contributes text
✔ hidden content is skipped during normal content traversal
✔ editing a shared label notifies every referencing node; unrelated text does not
✔ zero-width glyphs from fixture metrics are treated as not rendered
✔ dependencies of a computed name are recorded
✔ duplicate ids resolve to first occurrence; removing it shifts resolution
✔ removing a labelledby target falls back to aria-label
✔ role change to a name-prohibited role empties the name
✔ nested controls contribute their alternative text
✔ broken references stay isolated per engine instance
✔ healing a broken reference recomputes only that instance
✔ cache entries are revalidated against tree version history
ℹ tests 16
ℹ pass 16
ℹ fail 0
```
