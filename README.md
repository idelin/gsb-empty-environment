# tailcall：带尾调用优化的小型表达式语言

用 Python 3.11（仅标准库）从零实现的表达式语言：词法、语法、解释执行全部
自行实现，**不使用 `eval` 或任何现成编译器**。核心特性是**真正的尾调用优化**：
识别尾位置调用并复用帧，同时保留可读的逻辑调用轨迹；`finally` 与待完成运算
会阻止不安全的优化。

## 语言概览

```
# 注释
def fact(n, acc) = if n == 0 then acc else fact(n - 1, acc * n)

let x = 40 in
let y = 2 in
try 0 + fact(x / y, 1) finally emit("清理")
```

- 值：整数、布尔（`true`/`false`）、字符串；`/` 为整除
- 运算：`+ - * / %`、比较 `== != < <= > >=`、短路 `and`/`or`/`not`、一元 `-`
- 结构：`def` 函数定义、`let ... in`、`if ... then ... else`、`try ... finally`
- 内建：`emit(v)` 把 `v` 追加到输出日志并原样返回
- 所有 AST 节点与错误的源码位置都是 **UTF-8 字节区间** `[start, end)`

## 尾位置规则

| 位置 | 是否尾位置 |
| --- | --- |
| 函数体 / 主表达式 | 是 |
| `if` 的两个分支、`let` 的体、`and`/`or` 的右操作数 | 继承外层 |
| `try` 的体 | **否**（`finally` 是待完成的清理动作） |
| `finally` 的体 | 是（清理动作已确定，复用安全） |
| 运算操作数、调用参数、`let` 的绑定值 | 否（存在待完成运算） |

因此 `1 + f()` 中的 `f()` 不是尾调用——加法还没完成，帧不能复用。

## 实现要点

- **显式状态机**（`tailcall/interpreter.py`）：控制项 + 续体栈（待完成运算）
  + 堆上帧栈。Python 调用栈恒为 O(1)，与语言层面的递归深度无关。
- **帧复用**：尾位置调用不压新帧，原地复用当前帧（`Frame` 被改写），
  `tail_reuse_count` 计数；非尾调用才压入新帧与 `KPopFrame` 续体。
- **逻辑调用轨迹**：`LogicalEntry` 记录每个帧的完整逻辑占用史
  （`fact(3, 1) ↻ fact(0, 6)（尾复用 ×3）`）；异常展开时逐帧记录，
  未捕获的错误附带完整逻辑调用栈。
- **finally 安全性**：`try` 体一律按非尾位置求值；异常展开（`_unwind`）
  沿途执行每个 `KFinally`，保证清理顺序（正常与异常路径一致）。

## 运行

```
python3.11 demo.py                        # 运行样例
python3.11 -m unittest discover -v        # 运行测试（21 个用例）
```

## 运行样例输出（`python3.11 demo.py`）

```
================================================================
1. 尾递归十万次：物理帧不增长（Python 递归上限临时调到 100）
================================================================
结果             : 5000050000
物理帧峰值       : 1
尾调用帧复用次数 : 100001

================================================================
2. 可读逻辑调用轨迹：fact(3, 1)（↻ 表示尾调用复用帧）
================================================================
  → #1 fact(3, 1)（新帧）
  ↻ #2 fact(2, 3)（复用帧）
  ↻ #3 fact(1, 6)（复用帧）
  ↻ #4 fact(0, 6)（复用帧）
  ← #4 返回 6
结果: 6

================================================================
3. 1 + f() 不是尾调用：待完成的加法阻止帧复用
================================================================
↻ #1 f(5)（复用帧）
  → #2 f(4)（新帧）
    → #3 f(3)（新帧）
      → #4 f(2)（新帧）
        → #5 f(1)（新帧）
          → #6 f(0)（新帧）
          ← #6 返回 0
        ← #5 返回 1
      ← #4 返回 2
    ← #3 返回 3
  ← #2 返回 4
结果: 5 ，物理帧峰值: 6 （随递归深度增长）

================================================================
4. try/finally：清理顺序必须保持（正常与异常两种情形）
================================================================
正常执行输出顺序:
  leaf: 业务完成
  cleanup: guarded 清理
  cleanup: 外层清理
异常展开输出顺序:
  cleanup: inner 清理
  cleanup: outer 清理
错误: 除数为零

================================================================
5. 错误报告：UTF-8 字节区间 + 逻辑调用轨迹
================================================================
除数为零（字节区间 68..73）
逻辑调用轨迹（最近的调用在最前）：
  <主程序>: boom(10) ↻ boom(0)（尾复用 ×10）
字节区间 68..73 对应的源码: b'1 / 0'
```

## 测试结果（`python3.11 -m unittest discover -v`）

```
test_string_token_span_with_multibyte ... ok
test_and_or_short_circuit ... ok
test_arithmetic_and_precedence ... ok
test_arity_error ... ok
test_call_non_function ... ok
test_let_and_emit ... ok
test_string_values ... ok
test_syntax_error_has_span ... ok
test_undefined_name ... ok
test_accumulator_version_is_tail ... ok
test_error_keeps_logical_trace_non_tail ... ok
test_error_keeps_logical_trace_tail ... ok
test_finally_cleanup_order_normal ... ok
test_finally_cleanup_order_on_error ... ok
test_finally_value_is_discarded ... ok
test_if_let_and_or_are_tail_positions ... ok
test_logical_trace_is_readable ... ok
test_pending_computation_blocks_tail_call ... ok
test_spans_are_utf8_byte_ranges ... ok
test_error_span_slices_source_bytes ... ok
test_tail_recursion_100k_constant_physical_stack ... ok

Ran 21 tests in ~5s
OK
```

关键用例：

- `test_tail_recursion_100k_constant_physical_stack`：尾递归 100000 次，
  在 `sys.setrecursionlimit(200)` 下运行，物理帧峰值 = 1。
- `test_pending_computation_blocks_tail_call`：`1 + f()` 中帧数随深度
  线性增长（峰值 101），证明待完成加法阻止了帧复用。
- `test_finally_cleanup_order_normal / _on_error`：正常与异常两条路径
  下 `finally` 清理顺序一致（内层先于外层）。
- `test_spans_are_utf8_byte_ranges`：含中文注释与中文标识符时，节点
  span 能精确切出对应的 UTF-8 字节。

## 目录结构

```
tailcall/
  lexer.py         词法分析（UTF-8 字节区间）
  ast_nodes.py     AST 节点定义
  parser.py        递归下降语法分析
  interpreter.py   显式状态机解释器（帧复用 / 逻辑轨迹 / finally 展开）
  errors.py        LangError（带字节区间与逻辑轨迹）
demo.py            运行样例
tests/             unittest 测试套件
```
