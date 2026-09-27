# resource_checker

有限状态模型上的**资源归属不变量**检查器。Python 3.11，仅标准库，测试用 `unittest`。

验证对象仅限给定的有限状态模型：模型是一张带标签控制流图，节点携带一个操作
（`acquire` / `release` / `transfer` / `borrow` / `end_borrow` / `use` / `alias` /
`maybe_alias` / `nop`），边分为 `normal` / `exception` / `finally` / `cancel` 四类。
检查器核验：**所有正常、异常与取消退出**上，每个已获取资源都被其**当前拥有者**
恰好释放一次，并给出未释放路径作为反例。

## 设计要点

- **逐资源归属，禁止全局引用计数**：抽象域为每个资源显式记录
  `owned(owner) / released / maybe_released` 与活跃借用集合。`transfer(src, dst)`
  通过移动 `owner` 字段并把源变量标记为 `MOVED` 来建模所有权转移——
  转移后原拥有者再释放是 `release_after_move` 错误；`double_release` 是另一独立错误。
- **别名、借用、转移**：变量映射到指向集（points-to set）。`alias` 精确、
  `maybe_alias` 非确定（弱更新产生 `maybe_released` 不精确性）；借用期间禁止
  释放/转移（`release_while_borrowed` / `transfer_while_borrowed`）。
- **显式抽象域汇合**：汇合点采用显式幂集域——每个程序点保存一个抽象状态
  **集合**，汇合即集合并（`domain.StateSpace`），无 widening（有限模型上保证终止）。
- **异常处理图**：`finally` 与 `cancel` 边与普通边一样被搜索；异常清理路径上的
  泄漏一样报出。
- **可能泄漏 vs 确定泄漏，不同证据**：每个抽象候选经**有界具体状态重放**分类：
  - `definite`：该见证路径的**所有**具体运行都泄漏（must-leak），证据为具体轨迹；
  - `possible`：仅部分具体运行泄漏（取决于非确定选择），或重放超出界限无法定论，
    证据为抽象路径 + 泄漏运行的具体见证；
  - `filtered`：没有任何具体运行确认（如永不抛异常的节点上的异常边）——
    **误报被重放过滤**，从报告中移除。
- **未决语义**：抽象搜索超出步数界限时，总体结论保持 `pending`，绝不报 `safe`。
- **反例可重放**：每个 `definite` 发现都附带可被 `concrete.replay` 重新执行并
  复现的路径与具体证据（测试 `TestReplayableCounterexamples` 逐条重放验证）。
- **穷举核验可靠性**：对小型无环程序枚举**全部**执行路径并逐一具体重放，
  得到基真值（错误集、泄漏集），要求分析器结果与之精确一致
  （`exhaustive.cross_check`，无漏报、无确定的误报）。

## 运行

```bash
python3.11 -m resource_checker      # 运行全部示例 + 穷举交叉核验
python3.11 -m unittest discover -s tests -v
```

## 运行样例（节选）

```
=== exception_missing_cleanup: finally forgets to release
    verdict: violated (states explored: 6, complete: True)
  [definite] leak of r@n0 at exception exit 'EXC'
      path: n0 -[normal]-> n1 -[finally]-> fin -[exception]-> EXC
      evidence: replay: all 1 concrete run(s) leak; witness keeps n0#0 owned by x

=== release_after_move: old owner releases after transfer
    verdict: violated (states explored: 3, complete: True)
  [definite] release_after_move at n2
      path: n0 -[normal]-> n1 -[normal]-> n2
      evidence: replay: concrete run confirms release_after_move at n2

=== maybe_alias_leak: nondeterministic alias, conditional leak
    verdict: warning (states explored: 5, complete: True)
  [possible] leak of r@n0 at normal exit 'RET'
      evidence: replay: 1/2 concrete run(s) leak; witness keeps n0#0 owned by a

=== false_positive_exception: never-raising op with handler
    verdict: safe (states explored: 5, complete: True)
  [filtered] leak of r@n0 at EXC
      evidence: replay: path is concretely infeasible (false positive)

=== exhaustive cross-check (analyzer vs. concrete ground truth)
  clean_program: OK (1 paths, 1 concrete runs, 0 errors, 0 leaks)
  ...
  false_positive_exception: OK (2 paths, 1 concrete runs, 0 errors, 0 leaks)
cross-check: all OK
```

## 测试结果

```
Ran 23 tests in 0.003s
OK
```

覆盖：正常/异常/取消退出泄漏、finally 清理、转移后原拥有者释放、重复释放、
借用期间释放、use-after-move、可能泄漏证据、误报过滤、重放不确定保持 possible、
搜索未完保持 pending、反例可重放、循环覆盖泄漏、12 个示例程序的穷举交叉核验。

## 代码结构

- `resource_checker/model.py` — 有限状态模型（操作、四类边、退出汇点）
- `resource_checker/domain.py` — 显式抽象域（逐资源归属 + 幂集汇合）
- `resource_checker/concrete.py` — 具体语义与有界重放
- `resource_checker/analyzer.py` — 前向抽象解释 + 候选分类（definite/possible/filtered）
- `resource_checker/exhaustive.py` — 路径穷举与基真值交叉核验
- `resource_checker/examples.py` — 示例模型
- `tests/test_checker.py` — unittest 套件
