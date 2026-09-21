# minilang：小型命令语言的 CFG 与数据流分析

纯 Python 3.11 标准库实现（测试用 `unittest`）。词法、语法、解释器全部手写，
不使用 `eval` 或任何现成编译器。源码位置一律使用 **UTF-8 字节区间** `[start, end)`。

## 语言语法

```
program   := stmt*
stmt      := ID '=' expr ';'
           | 'print' expr ';'
           | 'if' '(' expr ')' block ('else' block)?
           | 'while' '(' expr ')' block
block     := '{' stmt* '}'
expr      := additive (('=='|'!='|'<'|'<='|'>'|'>=') additive)?
additive  := multi (('+'|'-') multi)*
multi     := unary (('*'|'/'|'%') unary)*
unary     := '-' unary | atom
atom      := NUM | ID | '(' expr ')'
```

`#` 到行尾为注释。整数语义，非零为真，除法向零取整。

## 目录结构

- `minilang/lexer.py` — 手写字法分析，token 携带 UTF-8 字节区间
- `minilang/parser.py` — 手写递归下降语法分析，AST 节点携带字节区间
- `minilang/cfg.py` — CFG 构建；常量条件折叠，死分支不连边
- `minilang/analysis.py` — 到达定义 / 一定已定义 / 诊断
- `minilang/interpreter.py` — 手写树遍历解释器
- `minilang/__main__.py` — 命令行入口
- `examples/` — 示例程序；`tests/` — unittest 测试

## 分析方法

- **CFG**：每个简单语句一个结点，每个 `if`/`while` 条件一个结点；
  循环体出口连回条件结点（回边），条件结点同时连向循环后继（零次循环路径）。
  条件为编译期常量时做常量折叠，不可走的边不连，由此产生不可达语句。
- **到达定义**（reaching definitions，may 分析）：`IN = ∪ OUT(p)`，
  `OUT = gen ∪ (IN − kill)`，工作list迭代至不动点（循环回边自然收敛）。
- **一定已定义**（definitely defined，must 分析）：`IN = ∩ OUT(p)`，
  `OUT = IN ∪ {本结点定义的变量}`。汇合取交集，因此：
  - 只在 `if` 一支赋值的变量，在分支后不保证已定义；
  - 循环条件结点处有“零次循环”入边，循环体内的赋值不会传播到循环之后。
- **诊断**：引用点处变量不在“一定已定义”集合 → `undefined-use`；
  从 entry 不可达的语句单独标记为 `unreachable`。

## 运行样例

```
python3.11 -m minilang examples/sample.mini      # 分析 + 执行
python3.11 -m minilang examples/undefined.mini   # 含诊断的示例
python3.11 -m minilang --analyze-only examples/undefined.mini
python3.11 -m minilang --run-only examples/sample.mini
```

`examples/sample.mini`（阶乘，含中文注释验证 UTF-8 字节区间）输出节选：

```
== 诊断 ==
  （无）
== 运行 ==
120
1
```

`examples/undefined.mini` 的诊断输出（静态警告与运行时错误相互印证）：

```
== 诊断 ==
  [undefined-use] 行 5:7 字节[39,40): 变量 'y' 在使用前可能未定义
  [undefined-use] 行 9:7 字节[78,79): 变量 'z' 在使用前可能未定义
  [unreachable] 行 11:5 字节[94,100): 不可达语句（assign）
== 运行 ==
2
运行时错误: 变量 'z' 未定义 @ 行 9:7 字节[78,79)
```

其中 `y` 只在 `if` 一支赋值，`z` 只在可能零次执行的循环体内赋值，
`w = 1;` 位于常量条件 `if (0)` 的死分支中。

## 测试结果

```
$ python3.11 -m unittest discover -v
...
Ran 36 tests in 0.004s

OK
```

覆盖：词法字节区间（含多字节字符）、语法与优先级、CFG 结构（回边、
零次循环出口、常量折叠剪枝）、到达定义不动点与 kill、单分支赋值/零次循环的
保守未定义诊断、不可达语句标记、解释器运算与控制流及运行时错误位置。
