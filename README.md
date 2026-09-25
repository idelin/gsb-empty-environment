# minilang：保持错误契约的表达式优化器

纯 Python 3.11 标准库实现的小型表达式语言：手写词法/语法分析器与树遍历解释器
（不使用 `eval` 或任何现成编译器），并在 AST 上执行三种优化——
**纯表达式常量折叠**、**分支简化**、**公共子表达式消除（CSE）**——
同时为每条优化输出**理由映射（rationale map）**。

## 语义契约

优化前后必须保持一致的可观察行为：

- **求值顺序**：二元操作数严格从左到右求值；第一个发生的错误即为程序错误。
- **短路语义**：`and` / `or` 的右操作数仅在左操作数不能决定结果时才求值。
- **错误位置**：每个运行时错误携带出错运算符/标识符在源码中的 **UTF-8 字节区间**
  `[start, end)`；优化后同一程序必须报告相同的错误消息与相同的字节区间。
- **输出序列**：`print` 产生的输出序列逐项一致。

由此推出两条核心约束：

1. `false and (1 / 0)` 可化简为 `false`——右操作数本来就不会被求值，
   删除它不改变结果与错误顺序（`true or X` 同理）。
2. 可能抛错的两个操作数**不能任意交换**（如 `(1/0) + (2/0)` 交换后错误位置
   会从左侧 `/` 变成右侧 `/`）。交换律仅用于编译期可证不抛错的纯操作数。

## 语言

```
stmt  := 'let' IDENT '=' expr ';'
       | 'if' expr '{' stmt* '}' ('else' '{' stmt* '}')?
       | 'print' expr ';'
       | expr ';'
expr  := or -> and -> 比较(非结合) -> 加减 -> 乘除模 -> 一元('-'/'not') -> 原子
原子  := INT | 'true' | 'false' | IDENT | '(' expr ')'
```

- 值：整数与布尔（两者不可混用，混用为类型错误）。
- 错误：除零/模零、类型错误、未定义变量；`#` 为行注释；标识符支持非 ASCII
  （如 `甲`），所有位置信息均为 UTF-8 字节区间。
- 整除 `/` 为向下取整的整数除法。

## 优化规则与安全论证

| 规则 | 内容 | 为何不违反契约 |
| --- | --- | --- |
| `constant-fold` | 纯字面量子表达式编译期求值（`2 + 3 * 4` → `14`） | 结果与运行期相同且不产生错误 |
| `short-circuit-fold` | `false and X`→`false`、`true or X`→`true`、`true and X`→`X`、`false or X`→`X` | 被删的 X 在短路语义下本就不会被求值，即使 X 会抛错 |
| `fold-skip` | 会抛错的常量表达式（`1 / 0`、`1 + true`）**故意不折叠** | 让错误仍在运行期按原顺序、原字节区间抛出 |
| `branch-simplify` | 常量条件的 `if` 只保留必走分支；空分支 `if` 退化为条件表达式语句 | 死分支的错误本就不会发生；拼接时用 `Block` 节点保持 `let` 作用域；空分支退化仍保留条件求值（条件可能抛错） |
| `cse` | 复用先前 `let` 绑定过的相同表达式（`let a = x*2 + 1` 中 `x*2` 复用 `y`） | 该 `let` 先于本语句**无条件**执行，且其间无相关变量重新绑定（值编号 + 作用域失效），不引入/消除/重排任何错误；从不把表达式**提前**到可能不执行的点，也不跨分支泄漏 |

CSE 的结构键对交换律操作符（`+ * == != and or`）做顺序规范化，**仅当两个
操作数都经 `try_const` 证明不抛错时**；否则严格保持书写顺序。

理由映射中每条记录包含：`rule`、`span`（原始源码 UTF-8 字节区间）、
`before`、`after`、`reason`。

## 运行

```sh
python3.11 driver.py examples/demo.lang           # 优化 + 双版本运行 + 契约校验
python3.11 driver.py examples/demo.lang --json    # 理由映射以 JSON 输出
python3.11 driver.py examples/error.lang          # 错误位置契约演示
python3.11 -m unittest test_all -v                # 测试
```

## 运行样例

`examples/demo.lang`（节选驱动输出）：

```
== optimized program ==
let x = 14;
let y = (x * 2);
let a = (y + 1);          # CSE: x * 2 复用 y
print a;
print y;
{
  print x;                # if true 分支简化，Block 保持作用域
}
print false;              # false and (1 / 0) 短路化简
print true;               # true or (1 / 0) 短路化简
let 甲 = 5;
print 甲;
{
  print (甲 + 1);
}

== rationale map (11 entries) ==
  [0] constant-fold  bytes [87, 92) '3 * 4'      after: 12
  [1] constant-fold  bytes [83, 92) '2 + 3 * 4'  after: 14
  [2] cse            bytes [163, 168) 'x * 2'    after: y
  [3] fold-skip      bytes [333, 338) '1 / 0'    （死分支中的除零，不折叠）
  [4] branch-simplify ... 'if true { ... } else { ... }'
  [6] short-circuit-fold bytes [398, 415) 'false and (1 / 0)' -> false
  [8] short-circuit-fold bytes [474, 489) 'true or (1 / 0)'   -> true
  [9] constant-fold  bytes [532, 538) '10 / 2'   after: 5
  ...

== run: original ==   outputs: ['29', '28', '14', 'false', 'true', '5', '6']
== run: optimized ==  outputs: ['29', '28', '14', 'false', 'true', '5', '6']
== contract check ==  RESULT: PASS
```

`examples/error.lang`（错误顺序与位置契约）：

```
== run: original ==
  outputs: ['42']
  error:   division by zero  at bytes [136, 137)  ->  '/'
== run: optimized ==
  outputs: ['42']
  error:   division by zero  at bytes [136, 137)  ->  '/'
== contract check ==  RESULT: PASS
```

`(1 / 0) + (a / 0)` 的两个操作数都可能抛错，优化器不交换它们，
错误仍发生在左侧 `/` 的字节区间 `[136, 137)`。

## 测试结果

```
$ python3.11 -m unittest test_all
....................................
----------------------------------------------------------------------
Ran 36 tests in 0.006s

OK
```

覆盖：UTF-8 字节区间（含 CJK 标识符）、词法/语法错误位置、运算优先级、
短路语义、从左到右错误顺序、常量折叠与 fold-skip、四种短路化简、
分支简化（含作用域保持与空分支退化）、CSE（复用/重绑定失效/作用域不泄漏）、
抛错操作数不交换、20 个程序的优化前后契约一致性（输出 + 错误消息 + 错误字节区间）。

## 文件

- `nodes.py` — AST 节点（span 均为 UTF-8 字节区间）
- `lexer.py` — 手写词法分析器（字节偏移表）
- `lang_parser.py` — 手写递归下降语法分析器
- `interp.py` — 树遍历解释器（左到右求值、短路、带位置的错误）
- `optimize.py` — 三种优化 + 理由映射
- `driver.py` — 驱动：优化、双版本运行、契约校验
- `test_all.py` — unittest 套件（36 个测试）
- `examples/demo.lang`、`examples/error.lang` — 样例程序
