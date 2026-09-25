# Pure Expression Optimizer

使用 Python 3.11 标准库实现的小型纯表达式语言，包含手写 UTF-8 词法分析、递归下降语法分析、解释执行、常量折叠、分支简化和公共子表达式消除。没有使用 `eval`、`exec`、`ast` 模块或现成编译器。

## 语言与契约

- 字面量：整数、`true`、`false`
- 变量：ASCII 或 Unicode 标识符；变量在单次求值中不可变
- 运算：`+ - * / %`、`< <= > >= == !=`、`!`、`&&`、`||`、条件表达式 `c ? a : b`
- `/` 和 `%` 的除数为 0 时抛出 `DivisionByZero`
- 所有源码区间都是 UTF-8 字节区间 `[start, end)`
- 求值顺序严格从左到右；`&&` 和 `||` 保留短路
- `false && (1/0)` 可简化为 `false`；`true || (1/0)` 可简化为 `true`
- 不交换任何可能抛错的操作数；公共子表达式只在其值已保证可用时复用
- 优化前后的值、错误类型和错误字节区间一致

优化后的公共子表达式以内部 IR 表示：

```text
@cse1(x + 1) + cse1
```

`@id(expr)` 在第一次出现的位置照常求值并缓存，`id` 在后续同作用域出现处读取缓存。它不是源码语法，应直接通过返回的 AST 执行。

## 文件

- `pure_expr.py`：AST、Lexer、Parser、Evaluator、优化器和理由映射
- `test_pure_expr.py`：19 个 unittest 测试
- `demo.py`：运行样例并输出完整 JSON
- `demo_output.json`：保存的样例输出

## 运行

```sh
python3.11 -m unittest -v
python3.11 demo.py
```

## 样例结果

```text
arithmetic folding
  source     : 1 + 2 * 3
  optimized  : 7
  contract   : {'before': {'result': 7}, 'after': {'result': 7}, 'matches': True}
false short-circuits division
  source     : false && (1 / 0)
  optimized  : false
  contract   : {'before': {'result': False}, 'after': {'result': False}, 'matches': True}
true short-circuits division
  source     : true || (1 / 0)
  optimized  : true
  contract   : {'before': {'result': True}, 'after': {'result': True}, 'matches': True}
constant conditional
  source     : false ? 1 / 0 : 40 + 2
  optimized  : 42
  contract   : {'before': {'result': 42}, 'after': {'result': 42}, 'matches': True}
common subexpression
  source     : (x + 1) + (x + 1)
  optimized  : @cse1(x + 1) + cse1
  contract   : {'before': {'result': 82}, 'after': {'result': 82}, 'matches': True}
erroring common expression
  source     : (x / 0) + (x / 0)
  optimized  : @cse1(x / 0) + cse1
  contract   : {'before': {'error': 'DivisionByZero', 'span': [3, 4]}, 'after': {'error': 'DivisionByZero', 'span': [3, 4]}, 'matches': True}
utf-8 positions
  source     : 值 + 1
  optimized  : 值 + 1
  contract   : {'before': {'result': 42}, 'after': {'result': 42}, 'matches': True}
```

## 理由映射

`optimize(source)["reasons"]` 包含：

- `ordered`：按优化执行顺序排列的完整理由
- `by_id`：`cf-n`、`bs-n`、`cse-n` 到理由对象的映射
- `by_span`：UTF-8 字节区间（如 `"0:7"`）到优化 ID 列表的映射
- `by_kind`：按 `constant_fold`、`branch_simplification`、`common_subexpression_elimination` 分类

常量折叠产生的除零不会在编译时抛出 Python 异常，而会生成保留原字节区间的错误 IR；因此运行优化后表达式时仍报告原来的错误位置。
