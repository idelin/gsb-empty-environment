# exact-lp-simplex

精确有理数（BigInt 分数）线性规划求解器：

```
max  c·x
s.t. A x <= b,  x >= 0
```

全程精确算术，不经过任何浮点近似；输出的分数全部约分。允许负 `b`、退化约束、
零变量、零约束、冗余行。

## 构建与测试

```sh
npm run build        # 需要任意 tsc >= 4.5（npm i -D typescript 或 npx tsc）
npm test             # 编译并运行全部测试
```

## 命令行

```sh
node dist/src/cli.js solve examples/optimal.json            # 求解
node dist/src/cli.js solve examples/optimal.json --budget 2 # 限制主元次数
node dist/src/cli.js check examples/optimal.json result.json # 独立验证证书
cat examples/optimal.json | node dist/src/cli.js solve -     # 从 stdin 读
```

输入 JSON：`{"c": [...], "A": [[...]], "b": [...], "budget"?: N}`，系数为整数或
分数字符串（`"3"`、`"-2"`、`"3/4"`）。

输出 JSON：

- `{"status":"optimal","value","x","y"}` — 原始解 `x` 与对偶 `y`
- `{"status":"infeasible","y"}` — Farkas 证书
- `{"status":"unbounded","x","d"}` — 可行点与上升射线
- `{"status":"budget_exhausted","pivots"}` — 迭代预算耗尽（不伪称不可行）

## 证书（由 `src/checker.ts` 独立验证，不调用求解器）

- **最优**：`x>=0`、`Ax<=b`、`y>=0`、`Aᵀy>=c`、`b·y == c·x == value`
- **不可行**：`y>=0`、`Aᵀy>=0`、`b·y<0`
- **无界**：`x` 可行、`d>=0`、`Ad<=0`、`c·d>0`

## 算法

`src/simplex.ts` 实现字典式两阶段单纯形法：

1. **第一阶段**：若某个 `b_i < 0`，引入人工变量构造辅助问题
   `max -x0`，把人工变量旋入最负的行得到可行字典；辅助最优值 `< 0`
   时不可行，此时松弛变量检验数的相反数即 Farkas 证书 `y`。
   若人工变量以 0 值留在基中，则将其旋出；整行为零说明约束冗余，删行
   （其对偶分量取 0，证书依然有效）。
2. **第二阶段**：恢复原始目标并继续迭代。检验数全 `<= 0` 时最优，
   对偶 `y_i = -(松弛变量检验数)`；某入基列无限比时无界，按字典给出射线 `d`。

所有主元选取使用 **Bland 规则**（最小下标入基、最小下标出基），从理论上
保证不会循环，退化顶点也能终止。`--budget` 限制主元总次数（含第一阶段），
耗尽时返回 `budget_exhausted`。

## 测试

- `test/fraction.test.ts` — 分数解析、约分、精确运算
- `test/simplex.test.ts` — 手工构造：零变量、零约束、负 b、冗余行、退化顶点、
  多最优解、Beale 循环实例（验证 Bland 规则终止）、预算耗尽、篡改证书被拒
- `test/random.test.ts` — 300 个随机小 LP：求解结果先过独立检查器，
  有界实例再与顶点枚举（`src/enumerate.ts`，精确解 n 元方程组）逐一比对
