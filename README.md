# recordlang

一个静态类型的小型语言，支持**记录类型、字段访问、字段更新、结构宽度子类型**，
并对**只读（`@ro`）与可写（`@rw`）位置做显式方差区分**。词法分析、递归下降解析、
类型检查与树遍历解释器全部手写（Python 3.11 标准库），不使用 `eval` 或现成编译器。
所有源码位置均以 **UTF-8 字节区间**（`[start, end)`）报告。

## 运行

```sh
python3.11 -m recordlang examples/points.rl     # 运行程序
python3.11 -m unittest discover -s tests -t .   # 运行测试
```

程序由若干语句组成：`let x = <expr>;` 绑定语句与 `<expr>;` 表达式语句
（表达式语句的值会被打印）。整个程序**先完整类型检查，全部通过后才解释执行**。

## 语法速览

```
stmt  := "let" IDENT "=" expr ";" | expr ";"
expr  := "let" IDENT "=" expr ";" expr
       | "if" expr "then" expr "else" expr
       | "extend" postfix "with" IDENT "=" expr      -- 显式扩展新字段
       | 比较/加减乘除/一元负号（常规优先级）
postfix := primary ("." IDENT | "with" IDENT "=" expr | "(" args ")")*
primary := INT | "true" | "false" | IDENT | "(" expr ")"
         | "{" [IDENT "=" expr, ...] "}"             -- 记录字面量
         | "fn" "(" [IDENT ":" type "@" mode, ...] ")" "->" type "{" expr "}"
type  := "Int" | "Bool" | "{" [IDENT ":" type, ...] "}"
       | "fn" "(" [type "@" mode, ...] ")" "->" type | "(" type ")"
mode  := "ro" | "rw"        -- 每个参数必须显式标注
```

注释为 `//` 到行尾。

## 类型规则

- **宽度子类型（协变，仅 `@ro`）**：`{x: Int, y: Int} <: {x: Int}`。
  含额外字段的记录可传给只读少字段参数；嵌套字段同样只读协变（深度子类型）。
- **可写位置不变（`@rw`）**：实参类型必须与形参**精确一致**，拒绝不安全的协变——
  因为被调用方可能通过该参数写入。
- **函数类型**：参数逆变（按各自声明的 mode 比较），返回值协变。
- **缺字段访问编译时拒绝**：`r.y` 要求 `y` 在 `r` 的静态类型中；只读视图之外的
  字段同样不可访问。
- **更新 vs 扩展**：`r with x = e` 只准更新已存在字段；字段不存在时必须用
  `extend r with x = e` 显式扩展；反之 `extend` 已存在的字段也会被拒绝。
- 更新/扩展均为纯语义：返回新记录，原记录不变。

## 样例

`examples/points.rl`：

```
$ python3.11 -m recordlang examples/points.rl
{x = 10, y = 0, z = 7}
17
{x = 0, y = 0}
```

`examples/width_subtyping.rl`（`get_x(point3)`，额外字段传给 `@ro` 参数）输出 `1`；
`examples/functions.rl`（高阶函数）输出 `42`。

编译时拒绝示例（注意错误位置为 UTF-8 字节区间）：

```
$ python3.11 -m recordlang examples/rejected_missing_field.rl
error: record of type {x: Int} has no field 'y': access to potentially missing
fields is rejected at compile time [bytes 106..107: 'y']

$ python3.11 -m recordlang examples/rejected_unsafe_covariance.rl
error: argument of type {x: Int, y: Int} does not match parameter of type
{x: Int} @rw: writable (@rw) parameters are invariant and do not allow width
subtyping; the argument type must match exactly [bytes 159..175: '{ x = 1, y = 2 }']
```

## 代码结构

- `recordlang/lexer.py` — 词法分析，token 携带 UTF-8 字节区间
- `recordlang/parser.py` — 递归下降解析，产出 `ast_nodes.py` 中的 AST
- `recordlang/typecheck.py` — 子类型判定（`is_subtype`，按 ro/rw 位置）与类型检查
- `recordlang/interpreter.py` — 树遍历解释器（闭包、记录为字典）
- `recordlang/api.py` — 程序级驱动：先整体类型检查再执行
- `tests/` — unittest 测试（词法字节区间、方差规则、缺字段拒绝、解释执行、示例）
