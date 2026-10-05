# bv-equiv-checker

固定位宽位向量表达式的等价性检查器（TypeScript 实现，零依赖）。

## 语义

- 所有表达式在同一个全局位宽 `width` 下求值，**所有结果按位宽截断**（包括常量与移位量）。
- 运算：位运算 `and / or / xor / not`，加减 `add / sub`（模 2^width 回绕），
  移位 `shl / lshr / ashr`，条件选择 `ite`（条件按"非零"判断）。
- **移位量 ≥ 位宽时结果固定为零**（对所有三种移位一致，无未定义行为）。
- `ashr` 在移位量小于位宽时按符号位扩展。

## 用法

```ts
import { add, v, c, checkEquivalence } from './src/index.ts';

const r1 = checkEquivalence(add(v('x'), v('y')), add(v('y'), v('x')), { width: 8 });
// { status: 'equivalent' }   —— 这是 SAT 求解器给出的证明，不是抽样

const r2 = checkEquivalence(add(v('x'), c(1)), v('x'), { width: 8 });
// { status: 'not-equivalent', counterexample: { x: 0n } }
// 反例是"无符号字典序最小"的输入：变量按名字排序，每个变量按无符号值比较

const r3 = checkEquivalence(a, b, { width: 64, budget: 1_000 });
// 预算（SAT 传播步数）耗尽 → { status: 'unknown' }
```

## 架构

- `src/expr.ts` — 表达式 AST 与构造器。
- `src/interp.ts` — **独立的具体解释器**（BigInt 求值），与符号路径不共享代码，
  用于验证反例与测试中的暴力枚举。
- `src/sat.ts` — 自包含的 CDCL SAT 求解器（first-UIP 子句学习、双监视字面量、
  VSIDS、几何重启），带共享步数预算，耗尽时抛 `BudgetExhausted`。
  支持假设（assumptions）增量求解，供反例逐位最小化使用。
- `src/bitblast.ts` — 符号约束：把表达式位爆炸（bit-blast）为 CNF
  （Tseitin 编码 + 常量折叠；加法器为行波进位；移位为桶形移位器 +
  `移位量 ≥ 位宽` 比较器强制归零）。
- `src/checker.ts` — 等价性判定：构造 miter（输出差异的析取），UNSAT 即等价；
  SAT 则按（变量名, 比特位, MSB 优先）顺序逐位最小化模型，得到字典序最小反例，
  并用独立解释器复核。

## 验证方式（证明而非随机测试）

`test/exhaustive.test.ts` 对表达式池（165 个表达式，覆盖全部运算符与 `ite`）
的**所有表达式对**在位宽 1–4 下做**穷举**交叉验证：

- 检查器 verdict 必须与解释器枚举全部输入的结果一致；
- 返回的反例必须与暴力枚举得到的字典序最小反例**完全相同**。

`test/units.test.ts` 覆盖解释器语义（截断、移位归零、符号扩展）、
已知恒等式证明、最小反例与预算耗尽返回 `unknown`。

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript type-stripping，无需编译）：

```sh
npm test            # node --test test/*.test.ts
npm run typecheck   # 可选：tsc --noEmit（需先 npm install）
```
