# paginate

固定页高、保持顺序的段落分页库（TypeScript）。输入每个段落各行的高度，
输出最优分页：先**最少页数**，再**最小各页剩余高度平方和**。

## 规则

- 页高固定，段落与行的顺序保持不变。
- 段落分裂时，页尾与页首至少各留 2 行（`MIN_SPLIT_LINES`）。
- 整段超过一页的段落允许分裂跨页。
- 单行超过页高：该行独占一页并溢出（`page.overflow === true`）。
- 不足 4 行、无合法分裂点且整段超页的短段：整段独占一页溢出。
- 严格分裂约束下无解时（如两行块也超页），自动放宽为允许单行分裂，
  此时每行都可独占一页，必然有解。
- 目标函数：先最小化总页数；页数相同时最小化
  `Σ max(0, pageHeight - usedHeight)²`（含最后一页）；
  仍平局时优先把前面的页填满，减少不必要的段落分裂。

## 用法

```ts
import { paginate } from './src/index.ts';

const result = paginate([[3, 3, 3, 3, 3], [2, 2]], 10);
// result.pages[i].segments -> { paragraphIndex, startLine, endLine }
// result.pageCount / result.remainingSquareSum
```

`paginateBruteForce`（≤24 行）以断点穷举独立实现同一套规则，用于核对。

## 运行与测试

无第三方依赖。直接运行 TypeScript 需要 Node ≥ 23.6（或 Node ≥ 22.6 加
`--experimental-strip-types`），或用任意 `tsc` 先行编译。

```sh
node --test test/*.test.ts   # 规则单测 + 短文档 DP vs 断点穷举交叉核对
```
