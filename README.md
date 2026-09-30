# exact-bound-judge

测量值携带误差界时对线性阈值约束做**精确**判定的 TypeScript 库。
全部数值以 BigInt 有理数表示，不经过 `Number` 中转；唯一的舍入点显式指定规则。

## 运行

```sh
npm test     # node --test test/*.test.ts （Node.js 内置测试运行器）
npm run demo # 运行 examples/demo.ts 样例
```

要求 Node.js ≥ 22.6（使用了原生 TypeScript 类型擦除；代码只含可擦除语法）。无第三方依赖。

## 判定语义

单个约束在测量误差盒（各变量区间之积）上分类：

| 判定 | 含义 |
|---|---|
| `pass`   | 整个误差盒都满足约束（按开/闭严格比较最坏角点） |
| `fail`   | 整个误差盒都违反约束 |
| `review` | 误差区间跨阈值，需复核——**绝不按中心值放行** |

多约束共享误差变量时，在**联合可行区域**上判定（精确 Fourier–Motzkin 消元），
不会把逐项“可能通过”合并成整体通过：

| 判定 | 证书 / 见证 |
|---|---|
| `pass`    | 每个约束的最坏角点与 LHS 最值 |
| `fail`    | Farkas 证书：源约束的非负组合导出 `0 <= 负数`（`verifyFarkas` 可独立核验）；或单约束全域失败证书 |
| `mixed`   | 可行域非空但不确定：精确有理数见证点（`verifyWitness` 可核验）+ 违反某约束的角点反例 |
| `pending` | 消元组合数超出预算（`Budget.maxCombinations`），不下任何结论 |

边界规则：

- 误差区间跨阈值 → `review`，不按中心值通过；
- 恰贴阈值 → 按约束开闭：`le/ge` 为闭（通过），`lt/gt` 为开（失败）；
- 误差未知（`'unknown'`）→ 区间无界，相关约束只能 `review`/`mixed`，**绝不当作 0**；
- 已知零误差是合法的精确测量。

## 舍入规则

`Rational.toDecimal(places, mode)` 是唯一可能丢精度的出口，`mode` 显式取值：
`'floor' | 'ceil' | 'trunc' | 'half-up' | 'half-even'`。
`Rational.fromDecimal` 精确解析十进制/科学计数法字符串（如 `"9007199254740993.125"`），
`fromInt` 拒绝非安全整数。

## API 概览

- `src/rational.ts` — BigInt 精确有理数（解析、运算、显式舍入）
- `src/measurement.ts` — 测量值（值 + 误差界 / `'unknown'`）、误差盒
- `src/constraint.ts` — 线性约束（`le/lt/ge/gt`）、规范化、精确求值
- `src/classify.ts` — 单约束三分类（pass/fail/review）与角点证书
- `src/joint.ts` — 联合可行性：FM 消元、Farkas 证书、见证点、预算
- `src/judge.ts` — 顶层判定：pass / fail / mixed / pending
- `src/enumerate.ts` — 小域有理网格枚举，核验边界行为
- `src/engine.ts` — 增量证据引擎：测量带版本号，修改后旧判定作废并归档历史

## 增量证据

`Engine` 为每个测量维护版本号；判定连同其依赖的版本一起缓存。
`setMeasurement` 更新测量时，依赖旧版本的判定被移入只增历史
（含原判定、依赖版本、作废原因），重新求值得到新判定。
未变更的测量不会触发重算；预算也是缓存有效性的一部分。

## 测试结果

```
$ npm test
ℹ tests 32
ℹ pass 32
ℹ fail 0
```

覆盖：大数精确解析与五种舍入规则、跨阈值不按中心值通过、恰贴阈值开闭、
未知误差不当 0、联合不可行的 Farkas 证书（独立核验）、混合区域见证与反例、
预算不足判 pending、枚举交叉核验边界、增量撤销与历史保留。
