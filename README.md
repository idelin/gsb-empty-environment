# handle-verifier

一个针对有限状态模型的句柄（handle）状态验证器，Python 3.11、仅标准库，
测试用 `unittest` 编写。

## 验证对象与结论

验证对象**仅限于给定的有限状态模型**（一个小型结构化 IR 编写的程序）。
结论有三种：

- `safe`：完整搜索后未发现错误；
- `unsafe`：发现错误，每条错误附带**可重放**的反例（分支选择序列）；
- `pending`：未搜索完（步数预算耗尽 / 循环不动点超上限 / 摘要不动点未
  收敛 / 调用了模型外的未知函数），保持未决，不下结论。

## 跟踪的状态与检查

每个资源有唯一身份；别名（`Assign`）共享同一资源身份，**不会**把多个
别名误当作多份独立的 open 资源。资源状态：`open` / `closed` / `moved`，
外加借用计数（共享读若干 / 独占写至多一个）。

检查项：

- 借用期间关闭（`close-while-borrowed`）
- 关闭后使用（`use-after-close`，含关闭后借用、关闭后转移）
- 转移后访问（`use-after-move`）
- 借用冲突（`borrow-conflict`：共享借出时写、独占借出时再借、
  所有者在借用期间写等）
- 重复关闭（`double-close`）、未绑定变量、非法操作

借用规则：共享读可并存，独占写互斥；`EndBorrow` 归还后资源恢复可用。

## 分析方法

- 每个程序点保留**可能配置集合**（别名映射 + 每资源状态），控制流汇合
  取并集，保守地保留所有可能所有权集合；别名经分支与循环传播。
- 循环在汇合语义下求不动点（配置空间有限，必终止，超上限则未决）。
- 跨函数转移用**方法摘要**：`(函数, 入参资源状态) -> 返回结果集合 +
  错误集合`；摘要本身按单调不动点迭代计算，支持递归函数。
- 错误按到达该点的配置计数区分 `definite`（每条路径都错）与
  `possible`（只有部分路径错，例如只在一支关闭、汇合后使用）。
- 反例记录为分支选择序列（`then`/`else`/`enter`/`exit`），具体执行器
  按序列重放必复现同类错误（`replay`）。
- 抽象解释器与具体执行器共用同一份语句语义
  （`handle_verifier/semantics.py`），保证两个引擎不会漂移。

## 目录结构

- `handle_verifier/model.py` — IR：Open/Assign/Borrow/EndBorrow/Close/
  Use/Move/If/While/Call/Return
- `handle_verifier/state.py` — 配置（别名 + 资源状态）与规范化
- `handle_verifier/semantics.py` — 单步语义（两个引擎共用）
- `handle_verifier/interpreter.py` — 抽象解释器：汇合、循环不动点、
  摘要不动点、definite/possible 判定
- `handle_verifier/concrete.py` — 具体执行器：反例重放、选择序列穷举
- `handle_verifier/enumerate.py` — 小有限程序随机生成与枚举核验
- `tests/` — unittest 测试
- `examples/run_samples.py` — 运行样例

## 运行

```sh
python3.11 examples/run_samples.py     # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 样例输出（节选）

```
== possible error: closed on one branch, used after the join
verdict: unsafe (steps=10)
  [possible] use-after-close at main#3: read x -- read of closed resource via 'x'
  replay use-after-close: OK (choices=('then',))

== cross-function transfer with recursion fixpoint
verdict: unsafe (steps=24)
  [definite] use-after-close at main#7: read y -- read of closed resource via 'y'
  replay use-after-close: OK (choices=('then',))

== pending: callee is not part of the finite model
verdict: pending (steps=6)
  incomplete: unknown function 'external_close'
```

## 测试结果

- `python3.11 -m unittest discover -s tests`：**38 个测试全部通过**
  （约 0.4s），覆盖生命周期、别名身份、借用纪律、控制流汇合、
  循环不动点、跨函数摘要、递归不动点、反例重放、pending 行为。
- 枚举核验（`tests/test_enumeration.py` 及更大规模压力运行）：
  对随机生成的小有限程序，比较抽象分析与具体穷举——
  800 个程序、22198 次具体执行，**0 个漏报、0 个虚报、0 个重放失败**；
  仅 3 个状态空间过大的程序按预期保持 `pending`（未搜索完不结论）。
