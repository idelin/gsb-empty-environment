# fsv — 有限状态验证器（Python 3.11，仅标准库）

把受限赋值/条件/循环程序展开为有限状态图并做显式状态验证。整数范围显式，
溢出语义可选（报错或模运算回绕）；执行器带有限堆、别名与异常清理；状态
规范化保留共享身份；每步证据锚定源码位置；反例由独立解释器重放。

## 语言

程序是语句元组（`fsv/lang.py`）：

- `assign(x, e)` / `store(a, i, v)`：赋值；`newarr(x, n)`：有限堆分配数组
- `iff(c, then, else)` / `while_(c, body)`：条件与循环
- `try_(body, final)`：异常清理（fault 不可捕获，finally 必执行）
- `assert_(c)`：断言，违反即 fault

构造器通过 `inspect` 自动捕获调用处的 `文件:行号` 作为源码位置。

## 语义要点

- **显式整数域** `IntDomain(lo, hi, mode)`：所有整数运算结果都落入 `[lo, hi]`。
  - `ERROR`：溢出产生 `overflow` fault；`WRAP`：按模运算回绕进域内。
  - 两种模式结果不同（见样例 1 与测试 `TestOverflowModes`）。
- **有限堆**：最多 64 个数组、每个最多 64 单元，超限报 `heap_overflow`。
- **别名**：变量可持有同一堆对象的 `Ref`，`a[0]:=5` 经 `b[0]` 可见。
- **错误**：`overflow`、`uninit_read`（未初始化读取）、`assert`、
  `index_out_of_bounds`、`div_by_zero`、`heap_overflow`、`type`。
- **状态规范化**（`fsv/state.py: normalize`）：按从环境可达的顺序重命名堆
  对象 id 并回收不可达对象。共享身份被保留——两个变量指向同一对象与指向
  两个等值对象规范化后仍然不同；仅 id 编号不同的同构状态被合并。
- **显式栈**：执行器（`fsv/executor.py`）用显式控制栈执行语句、用显式工作
  栈求值表达式，深控制流（数百层嵌套）不触发 Python 递归限制。
- **异常清理**：fault 沿控制栈展开，途中每个 `finally` 清理块都会执行，
  然后 fault 继续向外传播（`cleanup`/`propagate` 事件）。

## 展开、证据与验证

- `explore(program, dom, max_states, max_steps)`（`fsv/explore.py`）把程序
  展开为有限状态图：每条边带 `Evidence(loc, kind, desc)`，`loc_index`
  保留源码位置 → 状态集合的映射。
- `verify(...)`（`fsv/verify.py`）只对**给定有限状态模型**下结论：
  - `SAFE`：模型完全展开且无 fault 状态；
  - `UNSAFE`：fault 可达，返回最短反例轨迹，并由**独立解释器**
    （`fsv/replay.py`，独立实现的第二种语义）逐步重放确认；
  - `UNKNOWN`：预算耗尽、未搜索完，保持未决——预算终止绝不报告程序
    正常结束。无终止循环因状态有限而不动点收敛，分析器自身不会挂起。

## 运行

```sh
python3.11 -m unittest discover -v     # 运行测试（20 个）
python3.11 examples/run_samples.py     # 运行样例
```

## 样例输出（节选）

```text
== sample 1a: overflow as error (int[-8..7]/error)
   verdict: UNSAFE (states=3, steps=2)
   fault reachable: overflow: value 8 outside [-8, 7] @ run_samples.py:50
     0 [exec     ] run_samples.py:49        x := 7
        -> env{x=7} heap{}
     1 [exec     ] run_samples.py:50        FAULT overflow: value 8 outside [-8, 7]
        -> env{x=7} heap{} fault=overflow
   independently replayed: True

== sample 1b: overflow wraps (int[-8..7]/wrap)
   verdict: SAFE (states=4, steps=3)          # 7+1 回绕为 -8，断言成立

== sample 3: overflow fault runs finally cleanup
   verdict: UNSAFE (states=7, steps=6)
     2 [exec     ] run_samples.py:70        fault overflow raised; unwinding
     3 [cleanup  ] run_samples.py:69        enter finally cleanup
     4 [exec     ] run_samples.py:71        cleanup_done := 1
     5 [propagate] run_samples.py:70        fault overflow escapes
        -> env{cleanup_done=1, x=7} heap{} fault=overflow
   independently replayed: True

== sample 4a: endless loop, wrap mode (finite cycle -> fully explored)
   verdict: SAFE (states=33, steps=33)        # 不动点收敛，分析器不挂起
== sample 4b: same loop with a tiny state budget (must stay undecided)
   verdict: UNKNOWN (states=4, steps=3)       # 预算终止 -> 未决，而非正常结束
```

## 测试结果

```text
Ran 20 tests in 13.4s
OK
```

覆盖：两种溢出语义结果不同、未初始化读取错误、断言/越界 fault 且反例可
重放、别名经堆可见、规范化保留共享身份且合并同构状态、finally 在正常与
异常路径都执行、嵌套 try 双层清理、无终止循环不动点收敛不挂起、状态/步数
预算耗尽均为 UNKNOWN、200 层嵌套用显式栈、每步证据带源码位置、源码到状态
映射、独立解释器与执行器一致。

## 目录

```text
fsv/lang.py       AST 与自动捕获源码位置的构造器
fsv/domain.py     显式整数域与两种溢出语义
fsv/state.py      状态、有限堆、保留共享身份的规范化
fsv/executor.py   显式栈小步执行器（有限堆、别名、异常清理、每步证据）
fsv/explore.py    展开为有限状态图（预算、源码到状态映射）
fsv/replay.py     独立解释器，重放反例
fsv/verify.py     SAFE / UNSAFE / UNKNOWN 判定与最短反例
tests/test_fsv.py unittest 测试（20 个）
examples/run_samples.py 运行样例
```
