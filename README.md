# minihm — 从零实现的 Hindley–Milner 类型推断小语言

纯 Python 3.11 标准库实现（词法、语法、类型推断、解释执行全部手写，
不使用 `eval`/`exec` 或任何现成编译器）。测试使用 `unittest`。

## 特性

- 整数、布尔、函数、`let` 绑定的统一类型推断（Algorithm W）
- `let` 绑定的泛化（let-polymorphism）：恒等函数可在多种类型上复用
- λ 绑定变量不泛化（单态），与标准 HM 一致
- 合一（unification）执行 **occurs check**，自应用等无限类型被拒绝
- 类型冲突时输出**最小可解释约束链**（对约束 provenance 图做 BFS 最短路径），
  并定位双方约束来源
- 所有源码位置均为 **UTF-8 字节区间**（半开区间 `bytes start..end`）
- 手写树遍历解释器，类型检查通过后直接求值

## 语言语法

```
program := expr EOF
expr    := "let" IDENT "=" expr "in" expr
         | "if" expr "then" expr "else" expr
         | "fun" IDENT "->" expr
         | compare
compare := add (("==" | "<") add)*
add     := mul (("+" | "-") mul)*
mul     := app ("*" app)*
app     := atom atom*                -- 并置即函数应用
atom    := INT | "true" | "false" | IDENT | "(" expr ")"
```

`#` 开头为行注释；标识符允许 Unicode 字母。`+ - *` 为 `Int -> Int -> Int`，
`<` 为 `Int -> Int -> Bool`，`==` 为多态等值（两侧类型须一致，结果为 `Bool`）。

## 运行

```
python3.11 -m minihm examples/identity.mh
```

先推断并打印类型，再解释执行打印值；类型错误打印约束链并以退出码 1 结束。

## 运行样例（实际输出）

恒等函数在 `Bool` 与 `Int` 两种类型上复用（let 泛化）：

```
$ python3.11 -m minihm examples/identity.mh
type: Int
value: 1
```

高阶函数组合子：

```
$ python3.11 -m minihm examples/compose.mh
type: Int
value: 7
```

自应用 `fun x -> x x` 产生无限类型 `a = a -> b`，occurs check 拒绝：

```
$ python3.11 -m minihm examples/self_application.mh
error: cannot construct an infinite type (occurs check failed)
  `a` — type of parameter `x` (bytes 75..76)
  occurs in its own type `a -> b`
  forced by: function application (bytes 80..83)
```

布尔加整数：同时定位双方约束来源（`true` 与 `+`）：

```
$ python3.11 -m minihm examples/bad_add.mh
error: type mismatch — cannot unify `Bool` with `Int`
  forced by: left operand of `+` (bytes 61..65)
  constraint chain:
    `Bool` — boolean literal `true` (bytes 61..65)
      | left operand of `+` (bytes 61..65)
    `Int` — `+` requires `Int` operands (bytes 66..67)
```

跨越多条约束的错误（`true` 经应用传给 `y`，而 `y` 被 `+` 约束为 `Int`）：

```
$ python3.11 -m minihm examples/chain.mh
error: type mismatch — cannot unify `Int` with `Bool`
  forced by: function application (bytes 99..119)
  constraint chain:
    `Int` — `+` requires `Int` operands (bytes 110..111)
      | function application (bytes 99..119)
    `Bool` — boolean literal `true` (bytes 115..119)
```

Unicode 标识符，位置仍按 UTF-8 字节区间报告：

```
$ python3.11 -m minihm examples/unicode.mh
type: Int
value: 5
```

## 设计说明

- **位置**：`Source` 预计算字符索引→UTF-8 字节偏移映射，词法器直接产出
  字节区间 `Span`，之后所有 AST 节点、类型 `Origin`、约束 `Reason` 都携带它。
- **泛化**：`let` 处对值类型中"不在类型环境里自由出现"的变量做量化；
  每次引用时实例化出全新变量（保留原 `Origin` 以便报错）。
- **occurs check**：绑定变量前检查其是否出现在目标类型中，
  出现则报 `InfiniteType` 并给出变量来源与触发约束的位置。
- **约束链**：每次合一（包括失败的那次）都在两个类型节点间记录一条
  带 `Reason` 的边；报错时在两个冲突类型之间做 BFS 取最短路径，
  即"最小可解释约束链"，逐条打印每个类型的来源与每条约束的位置。

## 目录结构

```
minihm/
  source.py    UTF-8 字节区间 Span 与 Source
  lexer.py     手写词法器
  astnodes.py  AST 节点（均带 Span）
  parser.py    递归下降语法分析
  types.py     类型、类型方案、Origin/Reason
  unify.py     合一 + occurs check + provenance 图最短路径
  infer.py     Algorithm W（推断 + let 泛化）
  interp.py    树遍历解释器
  report.py    约束链错误渲染
  cli.py       命令行入口
tests/         unittest 测试（41 个）
examples/      示例程序
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -t .
.........................................
----------------------------------------------------------------------
Ran 41 tests in 0.004s

OK
```
