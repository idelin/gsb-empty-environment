# grid-track-sizing

TypeScript 实现的网格列轨道尺寸分配引擎：固定（fixed）、内容（content）与比例（fraction）
三类轨道，支持跨列项最小宽度约束、列间隙（gap）与溢出诊断。节点树与字形度量均由夹具
给出，核心计算不借助任何浏览器布局引擎。比例轨道的分配是迭代式的，并且保证收敛：
每一轮迭代要么完成分配，要么至少冻结一条轨道，轨道数量有限，故必然在 `轨道数 + 1`
次迭代内必然终止。

## 要求

- Node.js >= 22.6（使用内置 `node:test` 与 `--experimental-strip-types` 直接运行 TS）

## 运行

```sh
npm test      # Node.js 内置测试运行器
npm run sample  # 运行样例
```

## 结构

- `src/glyphs.ts` — 夹具字形度量（advance 表），文本宽度与最小内容宽度（最宽不可断词）
- `src/tree.ts` — 节点树类型与校验（轨道定义、跨列项、容器）
- `src/tracks.ts` — 核心轨道尺寸分配：基线初始化 → 项最小宽度贡献（按跨度升序）→
  比例轨道迭代分配（冻结法，零权重安全）→ 溢出诊断
- `src/layout.ts` — 由轨道尺寸计算各项的位置与宽度
- `src/fixtures.ts` — 夹具节点树
- `test/` — `node:test` 测试；`examples/demo.ts` — 运行样例

## 关键行为

- 跨列项的最小宽度（最宽词）会约束其跨越轨道的总宽：先由单列项抬高各轨道基线，
  跨列项再对缺口在内容轨道（否则比例轨道）上均摊；仅跨固定轨道且放不下时报告
  item 级溢出诊断。
- 容器宽度不足时报告 container 级溢出（含缺口像素数），比例轨道收缩到下限，
  绝不产生负宽度。
- 零权重比例轨道只取其下限，总权重为零时不做除法，不会除零。

## 运行样例输出（`npm run sample`）

```
== mixed grid (container 800px, gap 10px)
  tracks: 160.00 | 35.20 | 383.20 | 191.60 (fraction iterations: 1)
  item logo   x=   0.00 width= 160.00 text=28.32
  item nav    x= 170.00 width=  35.20 text=68.64
  item hero   x= 215.20 width= 584.80 text=140.16
  item side   x= 215.20 width= 383.20 text=24.64
  item foot   x= 608.40 width= 191.60 text=25.28
  diagnostics: none

== spanning grid (container 400px, gap 20px)
  tracks: 225.52 | 154.48 (fraction iterations: 2)
  item wide   x=   0.00 width= 400.00 text=328.64
  item c0     x=   0.00 width= 225.52 text=8.00
  item c1     x= 245.52 width= 154.48 text=8.32
  diagnostics: none

== cramped grid (overflow) (container 100px, gap 10px)
  tracks: 80.00 | 60.00 | 0.00 (fraction iterations: 1)
  item a      x=   0.00 width=  80.00 text=24.00
  diagnostics: [container] container is 100px but tracks need at least 160px (overflow 60px)

== zero-weight fractions (container 500px, gap 0px)
  tracks: 0.00 | 40.00 | 100.00 (fraction iterations: 1)
  diagnostics: none
```

## 测试结果（`npm test`，Node v22.14.0）

```
1..15
# tests 15
# suites 0
# pass 15
# fail 0
# cancelled 0
# skipped 0
# todo 0
```

覆盖：固定轨道精确尺寸、内容轨道按最宽词度量、比例轨道按权重分配余量、
跨列项最小宽度约束总宽（含不等权重触发的冻结迭代）、容器不足时报溢出且无负宽、
零权重轨道不除零、仅跨固定轨道的项溢出诊断、迭代收敛轮数上界（轨道数 + 1）、
项位置与间隙对齐、非法容器校验。
