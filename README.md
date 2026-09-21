# minilang — 统一类型推断、let 泛化与解释执行

一个从零实现的小语言：自带词法分析、递归下降语法分析、Hindley–Milner 风格的
**统一类型推断 + let 泛化（含 occurs check）**，以及一棵语法树解释器。
仅使用 Python 3.11 标准库，不使用 `eval`、`exec` 或任何现成编译器。

## 语言概览

```
int 字面量   0, 1, 42
布尔         true, false
变量         x, 变量                （允许 UTF-8 标识符）
函数         fun x. e
应用         f arg                  （左结合，可连续应用）
let 绑定     let x = e1 in e2       （x 被泛化，可在不同类型上复用）
条件         if c then a else b
算术         + - * /
比较         < > <= >=
相等         == !=
注释         // 行注释
```

运算符优先级（低→高）：`== !=`，`< > <= >=`，`+ -`，`* /`；均为左结合。

## 运行

```sh
# 类型检查并运行（先打印推断类型，再打印求值结果）
PYTHONPATH=src python3 -m minilang examples/id_poly.ml

# 只做类型推断
PYTHONPATH=src python3 -m minilang examples/self_app.ml; echo $?

# 测试
./run_tests.sh
```

## 实现要点

- **源码位置全部是 UTF-8 字节区间**。Token/AST 携带半开区间 `[start, end)`，
  标识符与错误位置按字节计数（多字节字符占多个字节），见
  `src/minilang/lexer.py` 与 `src/minilang/report.py`。
- **类型推断**（`src/minilang/typer.py`）
  - 每个类型变量是并查集中的节点；`unify` 执行合一并记录每条等式约束。
  - **occurs check**：合一前递归检查变量是否出现在箭头类型内部，命中则拒绝
    自应用导致的无限类型。
  - **let 泛化**：在提升的 level 上推断右值，只冻结 level 大于当前层的变量；
    每次使用泛化值时实例化一份新的类型变量（`a -> a` 实例化后仍为 `a -> a`）。
    lambda 参数不泛化，因此跨类型使用会被拒绝。
- **最小可解释约束链**：约束被记录为一张图上的边（`unify` 等式边、`commit`
  绑定到 Int/Bool/箭头的边、`child` 箭头参数/结果边）。发生冲突时用 BFS 求
  从一个头符号到另一个头符号的**最短边序列**；occurs 失败时求包含至少一条
  `child` 边的最短环。每条边都带字节位置与一句说明，按推理顺序渲染。
- **解释执行**（`src/minilang/interpreter.py`）：词法闭包 + 树遍历，
  不做隐式类型转换；类型检查先行，运行期再做一次防御性检查。

## 三个关键场景

- 恒等函数在两个类型上复用：`let id = fun x. x in id 1 ... id true` 推断通过
  （见 `examples/id_poly.ml`）。
- 自应用产生无限类型：`fun x. x x` 与 `let f = fun x. x x in f f` 被拒绝，
  错误链沿“箭头参数 → 被当函数使用 → 应用等式 → 回到参数自身”展开
  （见 `examples/self_app.ml`）。
- 布尔加整数：`true + 1` 被拒绝，约束链同时给出布尔字面量、`+` 的同型
  约束、整数字面量三个来源及各自字节位置（见 `examples/bool_plus_int.ml`）。
