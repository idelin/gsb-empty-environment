# minilang — 带增量解析的小语言

纯 Python 3.11+ 标准库实现（无 `eval`、无第三方 parser）。手写词法器、
递归下降语法分析器、树遍历解释器，以及基于编辑范围的增量重解析。

## 语言

```
program  := decl*
decl     := 'fn' IDENT '(' params? ')' '=' expr
          | 'let' IDENT '=' expr
expr     := comparison
comparison := additive (('=='|'!='|'<'|'<='|'>'|'>=') additive)*
additive := multiplicative (('+'|'-') multiplicative)*
multiplicative := unary (('*'|'/') unary)*
unary    := '-' unary | postfix
postfix  := primary ('(' args? ')')*
primary  := NUMBER | IDENT | '(' expr ')'
          | 'if' expr 'then' expr 'else' expr
```

支持 `#` 行注释。所有源码位置均为 **UTF-8 字节区间** `[start, end)`。

## 特性

- **错误恢复产生明确错误节点**：同步 token（`fn`/`let`/`)`/`,` 等）从不被
  错误恢复吞掉，因此未闭合的括号/调用不会吃掉后续声明；错误以
  `ErrorNode` 进入语法树（`minilang.collect_errors` 收集）。
- **按编辑范围重用未变子树**：`Session.edit(start, end, replacement)`
  重解析时，满足以下条件的旧子树按对象身份直接复用（其余节点重建）：
  1. 严格位于编辑范围之外（贴边的节点需以空白分隔，避免 token 合并）；
  2. 新源码对应位置文本逐字节相同；
  3. 后继 token 与旧解析相同；
  4. 不是解析循环的中间节点（如 `f(1)(2)` 的内层调用、`a+b+c` 的前缀）。
  解析器是确定性单 token 前瞻，上述条件保证复用节点与全量重解析一致。
  编辑后节点的字节偏移随 delta 原地平移（旧树应丢弃，同 tree-sitter）。
- **全量与增量树语义一致**：随机编辑压力测试（400+ 用例 × 多 seed）
  验证 `Session.edit` 结果与 `parse` 全量解析结构相等（含所有 span）。

## 用法

```python
import minilang as ml

env = ml.run("fn fact(n) = if n < 2 then 1 else n * fact(n - 1)\n")
print(ml.evaluate_expression("fact(5)", env))   # 120

session = ml.Session("fn a(x) = x * 2\nfn b(y) = y + 10\n")
tree = session.edit(24, 26, "200")              # 增量编辑（字节区间）
print(session.reused)                           # 被复用的旧节点
```

## 运行

```
python3 demo.py                          # 运行样例
python3 -m unittest discover -s tests    # 测试
```

## 代码结构

- `minilang/lexer.py` — 字节级词法器，token 带 UTF-8 字节 span
- `minilang/ast_nodes.py` — AST 节点（span 可变以支持平移）、结构相等、dump
- `minilang/parser.py` — 递归下降解析 + 错误恢复 + 复用钩子
- `minilang/incremental.py` — `Session`/`parse`，候选子树筛选
- `minilang/interpreter.py` — 树遍历解释器（函数、递归、运行时错误带 span）
- `tests/test_minilang.py` — unittest 套件（含随机增量一致性测试）
- `demo.py` — 运行样例
