# terminal-layout

等宽终端式布局引擎（TypeScript，零依赖，Node.js 内置 type stripping 直接运行）。

## 运行

```sh
npm test    # node --test（Node.js 内置测试运行器）
npm run demo
```

要求 Node.js >= 23.6（默认启用 `--experimental-strip-types`）。

## 结构

- `src/measurer.ts` — `Measurer` 接口：布局引擎按**调用方提供**的字形簇边界与宽度执行；
  附带默认实现（`Intl.Segmenter`  grapheme 边界 + 宽字符 2 列 / 零宽组合符 0 列）。
- `src/pieceTable.ts` — 可编辑片段表（piece table），公开偏移为 UTF-16，编辑自动避开代理对中间。
- `src/layout.ts` — 片段树布局：`document → line → row(软折行) → fragment(cluster|tab)`。
  - tab 扩展到下一制表位（制表位相对每个软折行行首；tab 宽 4 时 `a` 后 tab 到列 4）。
  - 簇原子不可拆：宽字符不跨折行列，零宽组合符不独占列、不单独触发折行。
  - `positionAt`（逻辑→视觉）/ `hitTest`（视觉→逻辑）双向转换；
    一个视觉位置有多个逻辑候选时按 `forward`/`backward`/`nearest` 策略返回
    **带方向偏好（affinity）的候选**，不伪造双射。
  - `LayoutEngine.applyEdit` 局部测量更新：只重测编辑触及的逻辑行，
    单行高度变化不会重测无关行（行数据按引用共享）；旧快照不可变，可继续命中测试。
  - 行终止符：LF、CRLF（以及孤立 CR）。

## 不变量（由测试保证）

- 任何 API 都不返回代理对中间的偏移。
- 随机编辑模糊测试：增量布局与全量布局 `computeFullLayout` 逐片段一致。
- 点击双宽字符第二列，`forward` 策略落在该字符起点。
