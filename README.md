# fixed-height-paginator

TypeScript 分页库：段落给出每一行的高度，库把文档切进固定高度的页，保持行顺序。

## 规则

- **保序 + 固定页高**：每页行高总和不超过 `pageHeight`，行顺序与输入一致。
- **段落分裂两行规则**：段落被分页拆开时，页尾片段和页首片段都至少 2 行（无孤行/寡行）；跨页的每个中间片段同样至少 2 行。
- **整段超页**：段落比一页还高时，按需拆到多页，两行规则仍然生效。
- **单行超页**：某一行自身超过页高时，独占一页并溢出（`overflow: true`），作为硬分页点；它被切开的两侧段落片段按独立段落处理。
- **退化输入**：若两行规则在数学上不可满足（例如同一段落的任意两行都放不进一页），库仍然返回结果，并通过 `violations` 报告被迫违规的页数。

## 目标（字典序）

1. 违规页数最少（正常输入恒为 0）；
2. **页数最少**；
3. **剩余高度平方和最小**（`Σ (pageHeight − usedHeight)²`，让各页填充更均衡）。

## 用法

```ts
import { paginate } from "./src/paginate.ts";

const result = paginate(
  [
    [4, 4, 4, 4], // 段落 0：4 行，每行高 4
    [3, 25, 3],   // 段落 1：中间行高超页 -> 独占一页溢出
  ],
  10,             // 固定页高
);
// result.pages       -> [{ chunks: [{ paragraph, startLine, endLine }], usedHeight, overflow }, ...]
// result.pageCount   -> 页数
// result.violations  -> 被迫违反两行规则的页数
// result.remainingSquareSum -> 剩余高度平方和
```

## 验证

`test/verify.ts` 做三层校验：结构不变量（保序、完整覆盖、页高约束）、手工边界用例、
以及与 `test/brute.ts`（断点集合穷举）在 1200 篇随机短文档上逐目标对拍。

```sh
node test/verify.ts   # 需要 Node >= 23.6（原生擦除类型），或先用 tsc 编译
```
