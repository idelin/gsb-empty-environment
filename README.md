# handlecheck — 有限状态句柄生命周期验证器

用 Python 3.11（仅标准库）实现的句柄状态验证器。对一个**给定的有限状态模型**
（句柄上的小程序：open / close / 借用 / 移动 / 别名 / 分支 / 循环 / 函数调用）
做保守的抽象解释，检查：

- `use-after-close`（关闭后使用）、`double-close`（重复关闭）
- `close-while-borrowed`（借用期间关闭）、`borrow-after-close`
- 借用规则：共享读可共存、独占写互斥（`write-while-shared-borrowed`、
  `write-through-shared-borrow`、`read/write-while-mut-borrowed`、
  `shared-borrow-while-mut`、`mut-borrow-while-shared`、`double-mut-borrow`）
- `use-after-move`（转移后访问）、`move-while-borrowed`

## 设计要点

- **别名共享同一资源身份**：资源身份为 `(open 站点, k)`（k 限量 widening
  保证域有限）。别名变量的 env 项指向同一身份，绝不会把多个别名分别标
  open 误当独立资源（`state.py`）。
- **控制流汇合保守**：`If`/`While` 汇合取 may-集合并（状态可能集、所有权
  可能集、借用可能集都保留），只在一支关闭时，汇合后的使用报*可能错误*
  （`analyzer.py` 的 `AState.join`）。
- **循环不动点**：循环体迭代至状态收敛；超出 `max_loop_iters` 仍未收敛则
  抛出 `Pending`，结论保持**未决**，绝不谎报安全。
- **方法摘要与跨函数转移**：按抽象输入签名缓存函数摘要，实参-形参共享
  资源身份，callee 的关闭/移动/借用效应回传 caller；`Return` 跨函数转移
  句柄。递归通过对摘要做外层不动点求解（`get_summary` / `analyze`）。
- **可能反例 + 可具体化状态**：每个错误带抽象见证轨迹；`concrete.confirm`
  用穷举的具体语义为其找到**可重放的具体反例**及具体状态
  （`concrete.replay` 可确定性重放验证）。
- **未搜索完保持未决**：步数/循环/外层迭代任一预算耗尽，结论为
  `PENDING`；只有完整搜索且无错误才是 `SAFE`。
- **小有限程序枚举核验**：`enumerate_check.py` 枚举所有长度 ≤ N 的小程序，
  对照具体语义断言：分析器 SAFE ⇒ 具体无错；具体有错 ⇒ 分析器必报可能
  错误；每个可具体化的错误都能重放。

## 文件

- `model.py` — 语句/函数/程序 AST
- `state.py` — 抽象状态（may-集合、汇合、垃圾回收）
- `analyzer.py` — 抽象解释器（汇合、不动点、摘要、错误与轨迹）
- `concrete.py` — 具体语义：`replay` / `explore` / `confirm`
- `enumerate_check.py` — 枚举核验（`python3 enumerate_check.py [N]`）
- `examples.py` — 运行样例（`python3 examples.py`）
- `test_analyzer.py` — 30 个 unittest（`python3 -m unittest -v`）

## 运行样例（节选）

```
$ python3 examples.py
[UNSAFE] use after close
    - use-after-close: use of 'x' on closed (0, 0)
      replayable counterexample confirmed; concrete state: {'env': {'x': 0}, ... 'status': 'closed' ...}
[UNSAFE] close on one branch, use after merge
    - use-after-close: use of 'x' on closed (0, 0)        # 只一支关闭，汇合后使用 -> 可能错误
[SAFE] shared borrow returned: owner write ok             # 借用返回后状态恢复
[UNSAFE] alias: close via one alias, use via the other    # 别名共享同一资源身份
[UNSAFE] recursive closer (fixpoint), then use            # 递归摘要不动点
[PENDING] tiny step budget stays pending                  # 未搜索完，保持未决
```

## 测试结果

```
$ python3 -m unittest 2>&1 | tail -3
Ran 30 tests in 0.616s
OK

$ python3 enumerate_check.py 4
programs: 54465 (safe 52876, unsafe 1589, pending 0, skipped 0)
replayed counterexamples: 1647
failures: 0
```
