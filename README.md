# minicoro：自带语法与解释器的协程语言

纯 Python 3.11 标准库实现，从空仓库手写词法分析、递归下降语法分析与树遍历解释器，
**不使用 `eval` / `compile` 或任何现成编译器**。所有源码位置使用 **UTF-8 字节区间**。

## 目录结构

- `minicoro/lexer.py` — 字法分析，token 携带 UTF-8 字节区间 `start..end`
- `minicoro/ast_nodes.py` — AST 节点定义（含字节区间）与 `contains_yield` 静态判定
- `minicoro/parser.py` — 递归下降语法分析
- `minicoro/interpreter.py` — 树遍历解释器、`Coroutine` 状态机、内建函数
- `examples/demo.py` — 运行样例
- `tests/test_minicoro.py` — unittest 测试（15 个用例）

## 语言语法

```
fn counter(label, n) {        # 函数定义；体内出现 yield 即为协程函数
    let i = 0                 # let 声明；= 赋值（沿作用域链查找）
    while i < n {
        yield label + "#" + str(i)   # yield 是表达式：挂出值，恢复时收到 resume 送入的值
        i = i + 1
    }
    return label + " done"    # 协程返回值
}
```

- 值：整数、字符串、布尔（`true`/`false`）、`none`、列表 `[1, 2]`
- 运算：`+ - * / %`（整数除法）、`== != < <= > >=`、`and or not`、一元 `-`
- 控制流：`if / elif / else`、`while`、`break`、`continue`、`try / finally`、`return`
- 注释：`#` 到行尾；标识符支持非 ASCII（如中文）
- 内建函数：`print`、`resume(co[, v])`、`close(co)`、`state(co)`、`len`、`push`、`str`

## 协程语义

- 调用含 `yield` 的函数得到协程对象，**不会**立即执行函数体
- `resume(co, v)`：返回 `[done, value]`；`done=false` 表示挂出于 `value`，`done=true` 为**明确终态**
  （`value` 为协程返回值）；完成或关闭后再次 `resume` 仍返回终态，不报错
- 挂起时协程的完整栈帧（嵌套调用、循环、局部变量）被保留，恢复后继续执行
- 协程各自独立的作用域链，交错运行局部变量互不污染
- **运行中禁止重入**：`running` 状态下 `resume`/`close` 抛出 `ReentrancyError`
- `close(co)`：在挂起点展开清理栈，`finally` 恰好执行一次；重复 `close` 幂等
- 语言异常（如除零、未定义变量）同样展开 `finally` 清理栈后向外传播
- 状态机：`created → running → suspended / done / closed`

## Python API

```python
from minicoro import Interpreter, Step

it = Interpreter()
it.run("fn f() { let x = yield 1  yield x + 1  return 99 }")
co = it.invoke(it.get("f"), [])   # 调用协程函数，创建协程
co.resume()      # Step(done=False, value=1)
co.resume(10)    # Step(done=False, value=11)  —— 10 送入 yield 表达式
co.resume()      # Step(done=True,  value=99)  —— 明确终态
co.resume()      # Step(done=True,  value=99)  —— 完成后仍为终态
co.state         # 'done'
```

## 运行样例

```
$ python3.11 examples/demo.py
a: [false, "A#0"]
b: [false, "B#0"]
a: [false, "A#1"]
b: [false, "B#1"]
a: [false, "A#2"]
b 已结束, 再 resume: [true, "B done"]
state a: suspended  state b: done
g: [false, "g1"]
guarded: finally 清理执行
state g: closed  resume closed: [true, none]
e: [false, "ready"]
e: [true, "收到:你好"]
运行中重入被禁止: coroutine 'selfy' is already running: re-entry forbidden
Python 侧 resume: Step(done=False, value='Z#0')
Python 侧 resume: Step(done=True, value='Z done')
终态后再 resume: Step(done=True, value='Z done')
```

## 测试结果

```
$ python3.11 -m unittest tests.test_minicoro -v
test_close_runs_finally_once ... ok          # 关闭挂起协程，finally 恰好一次
test_exception_unwinds_finally_stack ... ok  # 异常展开嵌套 finally（inner→outer）
test_inlanguage_resume_pair_and_state ... ok # 语言内 resume 返回 [done, value]
test_interleaved_coroutines_isolated_locals ... ok  # 交错运行局部变量隔离
test_nested_function_yield_scope ... ok      # yield 归属最近函数
test_reentry_forbidden ... ok                # 运行中重入抛 ReentrancyError
test_suspended_stack_frames_preserved ... ok # 挂起保留栈帧（循环+嵌套调用）
test_terminal_state_after_finish ... ok      # 完成后 resume 返回明确终态
test_yield_and_send_values ... ok            # yield 挂出/送入值
test_yield_outside_coroutine_rejected ... ok
test_control_flow ... ok
test_return_outside_function_rejected ... ok
test_runtime_error_reports_byte_span ... ok  # 错误携带 UTF-8 字节区间
test_ast_byte_spans ... ok                   # AST 节点字节区间可切片还原源码
test_utf8_byte_spans ... ok                  # 含中文注释/字符串的 token 字节区间

Ran 15 tests in 0.004s
OK
```
