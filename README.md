# adt-match-checker

针对代数数据类型（ADT）模式匹配的 TypeScript 检查库：

- **穷尽性判定**：分支集合是否覆盖所有可能的值；
- **不可达分支**：哪些分支被前面的分支完全覆盖（冗余分支）；
- **最小深度见证**：未穷尽时给出构造深度最小的反例值；可达分支给出最小深度的命中见证；
- **递归类型安全**：类型按名引用、永不展开，符号算法只沿模式中出现的构造器递归，必然终止；
- **交叉验证**：用深度受限枚举独立验证符号算法的结论，并展示枚举意义下的不可达分支。

## 模型

```ts
// 类型环境：每个类型是有限构造器集合，字段按类型名引用（可递归、可互递归）
const env: TypeEnv = {
  Nat:  { name: "Nat",  ctors: [{ name: "Zero", fields: [] }, { name: "Succ", fields: ["Nat"] }] },
  List: { name: "List", ctors: [{ name: "Nil", fields: [] },  { name: "Cons", fields: ["Nat", "List"] }] },
};
```

模式支持构造器嵌套、通配符 `_` 与通配变量 `varp("x")`（检查语义与 `_` 相同）：

```ts
import { analyzeMatch, ctor, varp, _ } from "adt-match-checker";

const report = analyzeMatch(env, "List", [
  ctor("Nil"),
  ctor("Cons", _, ctor("Nil")),
]);
// report.exhaustive === false
// report.counterexample = Cons(Zero, Cons(Zero, Nil))  （最小构造深度 3）
```

## 算法

核心是 Maranget 的模式矩阵有用性（usefulness）算法（`src/matrix.ts`）：

- 矩阵 `P` 为已检查分支，向量 `q` 为待查模式；`useful(P, q)` 递归地对首列构造器
  做**特化**（specialization）或在签名不完整时取**默认矩阵**（default matrix）；
- 穷尽性 = 全通配向量对 `P` 有用；分支 `i` 不可达 = 第 `i` 行对前 `i-1` 行无用；
- 见证构造沿递归回代，通配位用该类型**最小深度值**填充；`minDepth(T)` 在有限类型
  图上做 Bellman-Ford 式松弛求最小不动点，因此递归类型不会无限展开；
- 在完整签名分支下对所有构造器取深度最小者，保证反例/见证的构造深度最小；
- 无有限值的类型（如 `type E = MkE E`）按不可居留处理：空匹配视为（空虚）穷尽。

终止性：每次递归要么减少列数，要么严格减小模式总规模，且只沿模式中实际出现的
构造器下潜（模式深度有限），与类型的递归性无关。

## 交叉验证

`src/enumerate.ts` 按构造深度逐层枚举所有值（递归类型只展开到深度界 `D`，带数量
上限），`src/check.ts` 中的 `crossValidate` 独立地用"首个匹配分支"语义重放每个值
向量，并与符号结论比对：

- 符号称穷尽 ⟺ 枚举值全部被匹配；
- 符号称分支不可达 ⟺ 枚举中无任何值首先命中该分支（报告中列出
  `unreachableUpToDepth`）；
- 非穷尽时，枚举到的最小未匹配深度必须等于符号反例深度（最小性交叉验证）。

任何不一致都会记入 `crossValidation.findings` 并将 `consistent` 置为 `false`。

## 运行

```sh
npm run build   # tsc -> dist/
npm run demo    # 8 个场景演示（含递归类型、嵌套模式、多 scrutinee）
npm test        # 14 个断言测试
```

> 离线环境说明：`node_modules/typescript` 是指向本机既有 TypeScript 5.2.2 的符号链接。

## API 一览

- `analyze(env, scrutineeTypes, rows, opts)`：通用入口（支持多 scrutinee）；
- `analyzeMatch(env, type, branches, opts)`：单 scrutinee 便捷入口；
- `useful(ctx, P, q, ts)`：底层有用性判定，返回见证模式向量或 `null`；
- `enumerateValues(ctx, types, maxDepth, cap)`：深度受限值枚举；
- `opts.enumDepth`（默认 6，≤0 关闭交叉验证）、`opts.enumCap`（默认 100000）。
