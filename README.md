# dim-check

公式的量纲（单位）类型检查器，TypeScript 实现，零依赖。

公式由数值、变量、加法、乘除法和整数次幂组成；每个变量携带一个
**基本单位指数向量**（如速度 `{m: 1, s: -1}`）。检查器用代数指数模型
推导每个子表达式的量纲，输出推导树；出错时报告**最小出错子表达式**。

## 运行

需要 Node ≥ 22.6（直接以 type-stripping 方式运行 `.ts`，无需编译）：

```sh
node src/cli.ts --var v=m/s --var t=s --var a=m/s^2 "v*t + 0.5*a*t^2"
node --test test/checker.test.ts
```

## 类型规则

| 规则 | 语义 |
| --- | --- |
| `Num` | 数值常量无量纲（`1`）。**零不多态**：`0` 不会隐式变成任意单位，`x[m] + 0` 是错误 |
| `Var` | 从环境取变量的指数向量 |
| `Add` | 两操作数量纲必须相同，结果为该量纲 |
| `Mul` / `Div` | 指数向量逐分量相加 / 相减 |
| `Pow` | 指数向量乘以整数幂次（支持负幂、零幂 → 无量纲） |

## 输出示例

成功时打印推导树与结论：

```
[Add] v * t + 0.5 * a * t^2 : m
  [Mul] v * t : m
    [Var] v : m/s
    [Var] t : s
  [Mul] 0.5 * a * t^2 : m
    ...
=> v * t + 0.5 * a * t^2 : m
```

失败时错误只标注在它产生的节点，并给出最小出错子表达式
（后序遍历找到的最深局部错误）：

```
error: unit mismatch in addition: m/s vs s
  minimal failing subexpression: v + t
```

## 代码结构

- `src/units.ts` — 指数向量代数（加、减、数乘、相等、格式化）
- `src/lexer.ts` / `src/parser.ts` — 词法与递归下降语法分析（`^` 支持 `x^-2`、`x^(-2)`）
- `src/ast.ts` — AST 与最小括号回显
- `src/check.ts` — 推导树构造、最小出错子表达式、树渲染
- `src/cli.ts` — 命令行入口（`--base` 声明基本单位，`--var x=kg*m/s^2` 绑定变量）
- `test/checker.test.ts` — 括号、负幂、无量纲、零常量等 13 个用例
