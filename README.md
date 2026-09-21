# minilang：小型命令语言的 CFG 数据流分析

纯 Python 3.11 标准库实现（无 eval、无现成编译器/解析器），包含：

- **词法器**：直接在 UTF-8 字节流上扫描，所有位置均为 UTF-8 字节区间 `[start, end)`；
- **语法分析器**：递归下降，生成带字节区间的 AST；
- **解释器**：树遍历执行（`let`/赋值/`print`/`if`/`while`/`halt`）；
- **CFG 构建**：每条语句一个节点，外加 entry/exit；
- **数据流分析**（均迭代至不动点）：
  - 定义到达（reaching definitions，may 分析，汇合取**并集**）；
  - 必然已定义（definitely-assigned，must 分析，汇合取**交集**）；
  - 使用前未定义诊断：无定义到达 → `error`；有定义到达但非全路径 → `warning`；
  - 不可达语句单独标记为 `info`，且不参与使用诊断。

## 语言语法

```text
program := stmt*
stmt    := "let" IDENT "=" expr ";" | IDENT "=" expr ";" | "print" expr ";"
         | "if" expr block ("else" block)? | "while" expr block | "halt" ";"
block   := "{" stmt* "}"
expr    := 比较(== != < <= > >=) → 加减 → 乘除模 → 一元(- !) → 整数 | 变量 | "(" expr ")"
注释    := "//" 至行尾（可含任意 UTF-8 字符）
```

## 保守性设计

- **分支汇合**：`if` 仅一支赋值的变量，汇合后不在 definitely-assigned 集合中（交集），
  使用后报 `warning`；
- **循环零次路径**：`while` 出口边直接从条件节点引出，循环体内定义不保证到达循环后；
- **常量条件折叠**：`if 0` / `while 0` 的死分支、`while 1` 的无出口、`halt` 之后的
  语句均标记为不可达；
- **循环不动点**：两个分析均在 CFG 上迭代至集合不再变化（回边使循环体内定义
  到达循环头）。

## 使用

```bash
python3 -m minilang examples/demo.ml            # 静态分析（诊断）
python3 -m minilang examples/demo.ml --cfg      # 额外打印 CFG
python3 -m minilang examples/demo.ml --sets     # 额外打印每个节点的数据流集合
python3 -m minilang examples/sum.ml --run       # 解释执行
python3 -m unittest discover -s tests -v        # 运行测试
```

## 运行样例

`examples/demo.ml`（注释含多字节 UTF-8 字符，验证字节区间）：

```text
$ python3 -m minilang examples/demo.ml
warning: 'b' may be used before definition (not assigned on all paths) (bytes 255..256, line 10 col 7)
warning: 'c' may be used before definition (not assigned on all paths) (bytes 407..408, line 15 col 7)
error: use of undefined variable 'u' (bytes 485..486, line 16 col 7)
info: unreachable statement (bytes 528..537, line 18 col 1)

$ python3 -m minilang examples/sum.ml --run
55
```

## 测试结果

```text
$ python3 -m unittest discover -s tests
........................................
----------------------------------------------------------------------
Ran 40 tests in 0.243s

OK
```

## 目录结构

```text
minilang/
  lexer.py        词法器（UTF-8 字节区间）
  ast_nodes.py    AST 定义
  parser.py       递归下降语法分析
  interpreter.py  解释执行 + 常量折叠
  cfg.py          CFG 构建（含不可达边剪枝）
  analysis.py     定义到达 / 必然已定义 / 诊断
  cli.py          命令行入口
examples/         示例程序
tests/            unittest 测试（40 个用例）
```
