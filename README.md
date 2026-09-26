# pagination-engine

约束感知的分页引擎（TypeScript，零依赖）。节点树与字形度量全部由夹具（fixture）给出，
不借助任何浏览器布局引擎：断行、度量、分页、脚注、证书全部在纯 TypeScript 中计算。

## 运行

需要 Node.js >= 23（直接运行 TypeScript，类型擦除）。

```sh
npm run sample   # 运行样例：分页 + 证书 + 脚注级联 + 局部更新
npm test         # Node 内置测试运行器（node:test），18 个测试
```

## 模型

- `src/model.ts` — 文档节点树：`paragraph`（行内 span，可带 `footnoteRef`）、
  `heading`（不可拆、跟随下一段）、`block`（不可拆块，给定高度）。
- `src/measure.ts` — 由 `GlyphMetrics` 夹具贪心断行；脚注文本同样度量出高度。
- `src/items.ts` — 把文档摊平为可放置项（行 / 标题 / 块）。

## 分页与约束（`src/paginate.ts`）

动态规划搜索最优分页，代价 = 空白代价（`slack^2`，末页豁免）+ 跨页断开罚分
（段落拆分罚分 + 违规罚分）。硬约束：

- 孤行/寡行：段落跨页时，前页至少 `orphans` 行、后页至少 `widows` 行。
  两行段落在 `orphans=widows=2` 下任何拆分都是 1/1，因此永远整体移动。
- 标题跟随（keep-with-next）：标题不得是页尾最后一项。
- 不可拆块：整体放置；若超过页容量，独占一页并标记 `overflow` 降级（明确报告）。
- 脚注随引用页占空间：脚注归属引用行所在页，脚注区（含分隔线）从该页容量中扣除；
  脚注变高会连锁把正文行推到后续页（见样例 "Footnote cascade"）。

### 降级策略（不循环、不默默违规）

约束按固定顺序单调降级：`keep-with-next` → `widows` → `orphans`，每条最多放松一次，
因此不会循环降级。所有放松都记入 `degradations`（约束名、页码、节点、原因）。
`src/validate.ts` 独立重算整篇布局：任何没有对应降级记录的违规都会报
"silently violates"，保证不会默默违规。单行超过页容量等硬不可行情况返回
`feasible: false` 与明确错误（明确无解）。

## 试排回滚与不动点

- `solveLevel` / `exhaustiveVerify` 即试排-回滚搜索：尝试候选页尾，失败即回溯；
  页序列严格非空，回溯不会产生空白页。
- `src/fixpoint.ts` — `resolveFixpoint` 反复"排版 → 独立校验"，页面边界签名收敛即
  不动点；签名循环或超限时明确报告无解。
- `src/update.ts` — 局部内容更正：`applyEdit` 替换节点后，`repaginateLocal`
  复用未受影响的前缀页，仅从首个分歧项重排；脚注高度变化会禁用前缀复用。
  结果与全量重排逐页一致并通过校验。

## 证书与穷举验证（`src/certificate.ts`）

对小文档做分支限界穷举：任何完整分页若代价低于引擎结果都会被找到
（`cheaperFound`），否则 `verified: true` 即证明引擎代价最优。证书逐页列出：
容量、正文/脚注占用、孤行/寡行检查、标题跟随检查、脚注归属。

## 样例输出（`npm run sample`）

```
== Pagination ==
page 0: body=72 footnotes=30 ids=[f1] slack=18
  heading(h1) line(p1#0) line(p1#1) line(p1#2) line(p1#3)
page 1: body=84 footnotes=0 ids=[] slack=36
  line(p2#0) line(p2#1) line(p2#2) line(p2#3) block(b1)
page 2: body=60 footnotes=18 ids=[f2] slack=42
  line(p3#0) line(p3#1) line(p3#2) line(p3#3) line(p3#4)
page 3: body=60 footnotes=0 ids=[] slack=60
  heading(h2) line(p4#0) line(p4#1) line(p4#2)
total cost: 3384, relaxation level: 0

== Degradations ==
none

== Certificate ==
page 0: capacity=120 body=72 footnote=30 items=5
  - capacity: 102 <= 120 ok
  - orphans: n/a (no paragraph continues onto this page)
  - widows/keep-with-next: n/a
  - footnotes: [f1] occupy 30
...（其余页略）
exhaustive: claimed=3384 nodesExplored=11 cheaperFound=0 verified=true truncated=false

== Footnote cascade (taller footnote pushes body lines) ==
page 0 body lines, short f1: 4
page 0 body lines, tall  f1: 2
page 0 footnote ids, tall f1: [f1]

== Local update + fixpoint ==
reused prefix pages: 1, fellBackToFull: false
new page count: 4, cost: 504
validation problems: none
fixpoint: converged in 1 iteration(s)
```

## 测试结果（`npm test`）

```
✔ certificate verifies engine cost exhaustively on the sample document
✔ exhaustive verification rejects an inflated cost claim
✔ certificate lists capacity, orphan checks and footnote attribution per page
✔ validator catches silent violations that lack a degradation record
✔ two-line paragraph is never split 1/1
✔ paragraph split respects orphans and widows minimums
✔ heading keeps with the following paragraph
✔ unsatisfiable keep-with-next degrades explicitly, not silently
✔ oversized unbreakable block is flagged as overflow without blank pages
✔ line taller than a page is an explicit no-solution
✔ footnote reserves space on its reference page
✔ taller footnote cascades body lines to later pages
✔ footnote is attributed to the page holding its reference line
✔ unknown footnote reference is an explicit error
✔ local edit reuses unaffected prefix pages and matches full re-pagination
✔ footnote growth invalidates reuse and still converges correctly
✔ fixpoint resolves for the sample document
✔ fixpoint reports explicit no-solution for infeasible input
ℹ tests 18
ℹ pass 18
ℹ fail 0
```
