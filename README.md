# mini-style-engine

一个不依赖浏览器的简化 CSS 计算样式引擎（TypeScript，Node ≥ 23.6 原生类型擦除运行，无第三方依赖）。

## 功能

- **简化选择器**：`div` / `.class` / `#id` / `*`、复合选择器、后代与子代组合器、逗号列表；条件选择器 `:hover` / `:focus` / `:active` / `:checked` / `:disabled` 及结构伪类 `:first-child` / `:last-child`。
- **特异性与层叠**：inline > (id, class, type) 特异性 > 来源顺序；低特异性后出现不能压过高特异性；同特异性按来源顺序后者胜。
- **继承与初始值分离**：继承属性取父节点计算值（`source: inherited`），非继承属性取初始值（`source: initial`），来源记录严格区分。
- **显式变量**：`--x: ...` 声明与 `var(--x, fallback)` 引用（回退可任意嵌套）；自定义属性随树继承，天然形成作用域变量（子树内覆盖）。
- **变量环与回退**：依赖环上的自定义属性按 css-variables 规则在计算值时刻失效（guaranteed-invalid）；使用处 `var()` 取回退，无回退则整条声明 IACVT，属性按 `unset` 处理（继承属性→继承，其余→初始值），而不是退而使用层叠中的下一条声明。回退路径记录失效条件：`reason: undefined | cycle | invalid` 及环路径 `cyclePath`。
- **每属性来源链**：每个计算值携带全部候选声明（选择器文本、特异性、规则序号、原始值）及结果（`won` / `lost-specificity` / `lost-order` / `invalid-at-computed-value-time`），外加 `var()` 解析轨迹。
- **依赖追踪与增量重算**：选择器匹配记录"咨询过哪些节点的属性"（类名/状态变化只重匹配相关节点）；计算条目（`节点|属性`）间维护反向依赖图（变量读取、继承）。修改祖先类名或变量时，只对脏闭包内的条目重算。
- **原子发布**：更新在旁路构建新样式表后整体换入并递增版本号；旧快照保持不变，读取方永远不会看到混合版本样式。
- **独立全量解释器对照**：`src/engine/reference.ts` 独立重实现匹配器、层叠排序与变量解析，对随机树 + 随机规则 + 随机修改（类名翻转、状态切换、内联变量、规则增删改）做逐步 deep-equal 对照，覆盖循环变量、继承覆盖、同特异性来源顺序。
- **夹具驱动布局**：节点树与字形度量由 `fixtures/glyphs.json` 给出，块盒垂直堆叠、按字形步进宽度贪心换行，核心计算不借浏览器布局引擎。

## 运行

```bash
nvm use          # 23.9.0（或任何 >= 23.6 的 Node）
npm test         # node --test（Node 内置测试运行器）
npm run demo     # 运行样例
```

## 结构

```
src/css/values.ts       var() 值标记化（嵌套回退）
src/css/selector.ts     选择器解析 / 特异性 / 匹配（带 consulted 追踪）
src/css/parser.ts       样式表解析
src/engine/computed.ts  计算值模型、层叠比较、来源链构建
src/engine/evaluator.ts 批量求值器：变量替换、环检测、IACVT
src/engine/engine.ts    增量引擎：依赖图、脏闭包、原子快照发布
src/engine/reference.ts 独立全量解释器（对照用）
src/layout/layout.ts    夹具字形度量下的块布局与文本换行
fixtures/glyphs.json    字形步进宽度夹具
test/                   node:test 测试（含随机差分对照）
examples/demo.ts        运行样例
```

## 运行样例（`npm run demo` 节选）

```
== 1. Computed styles with per-property source chains ==
desc#4 color (undefined var -> fallback -> scoped --accent from .card):
  color = "teal"  (source: {"kind":"declaration","ruleId":4,...,"raw":"var(--missing, var(--accent))"})
    [won] .desc { color: var(--missing, var(--accent)) }  spec=0,1,0 rule#4
    var(--missing) -> fallback = "teal" reason=undefined
    var(--accent) -> resolved = "teal"
loop#5 color (variable cycle -> fallback) and width (cycle, no fallback -> unset):
  color = "crimson" ...
    var(--a) -> fallback = "crimson" reason=cycle cycle=--a -> --b
  width = "auto"  (source: {"kind":"invalid","unsetTo":"initial"})
    [invalid-at-computed-value-time] .loop { width: var(--a) }  spec=0,1,0 rule#5

== 2. Incremental update: add theme-dark class to the root ==
  version 1 -> 2; recomputed 5 entries:
  1|--accent, 1|color, 3|color, 2|--accent, 2|color
  title color: rebeccapurple -> gold
  desc color (scoped .card --accent shadows root): teal
  old snapshot still reads the old value: rebeccapurple (version 1)

== 6. Fixture-driven layout (glyph metrics, no browser) ==
  node#1 x=0 y=0 w=80 h=132
    node#2 x=0 y=0 w=80 h=24 lines=["Hello"]
    node#3 x=2 y=26 w=76 h=108
      node#4 x=26 y=50 w=28 h=40 lines=["ab","ab"]
      node#5 x=26 y=90 w=28 h=24 lines=["ab"]
```

注意第 2 节：只重算了 `--accent` 及其依赖（`color`）共 5 个条目；`.card` 子树因作用域变量遮蔽而未受影响。

## 测试结果

`npm test`（Node v23.9.0）：**38 个测试全部通过**，其中差分测试对 100 个随机种子 × 25 步随机修改逐步对照增量引擎与独立全量解释器（每步全量 deep-equal），另含循环变量重压种子与定向不变量（同特异性来源顺序、低特异性不压高特异性、多层继承覆盖）。

```
ℹ tests 38
ℹ pass 38
ℹ fail 0
```
