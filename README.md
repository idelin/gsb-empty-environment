# 进程 ID 置换对称归约验证器

在**给定的有限状态模型**上，按进程角色对进程 ID 做置换规范化（symmetry
reduction），使用标准库 + `unittest` 实现，无第三方依赖，适用于 Python 3.11。

## 设计要点

- **同构进程只留一个代表**：相同角色的进程构成置换组，规范化代表取该组所有
  置换下按 `(shared, locals)` 字典序最小的状态（`symcheck/symmetry.py:54`）。
  两个同构进程交换局部状态后规范化为同一状态。
- **特殊角色不混同**：置换只在同角色组内进行。`"leader"` 是单元素组，
  永远不会与 `"worker"` 交换；共享变量中出现的进程 ID（如锁持有者）随置换
  一起改名，`NO_PID = -1` 视为“无进程”。
- **对称条件需验证**：`check_symmetry_condition` 在具体状态空间上检查迁移关系
  对同角色置换不变（`s --(pid,a)--> t` 蕴含
  `π(s) --(π(pid),a)--> π(t)`）。模型若把特殊迁移硬编码给 0 号进程却把所有
  角色都声明为 `worker`，检查会返回 `False` 和具体见证。
- **反例保留 ID 映射、可还原、可重放**：每个反例状态都附带
  `id_map`（原始 ID → 规范 ID）。用逆置换可从规范状态还原原始状态；反例是
  原始 ID 的具体状态 + 动作序列，`replay()` 从初态逐条执行即可重现。
- **三态结论**：`safe`（空间搜索完毕且不变式恒成立）、`violated`（找到违例，
  附反例）、`pending`（`max_states` 预算用完、搜索未完，不作安全结论）。

## 目录

- `symcheck/model.py` — 模型、状态、按进程索引的迁移；角色是显式声明的。
- `symcheck/symmetry.py` — 角色分组、置换枚举、规范化、对称条件检查。
- `symcheck/checker.py` — BFS 验证（可选对称归约）、反例重建、重放。
- `symcheck/examples.py` — 互斥模型（1 领导 + N 工人）、缺陷变体、隐藏领导
  的非对称模型。
- `tests/` — 15 个 `unittest` 用例。
- `run_demo.py` — 运行样例。

## 运行

```sh
python3.11 -m unittest discover -s tests -v
python3.11 run_demo.py
```

## 测试结果

```
Ran 15 tests in 0.003s

OK
```

覆盖：同构进程交换后代表唯一；领导不参与置换且领导/工人的临界态不会被
混同；共享变量中的持有者 PID 随置换改名；ID 映射可逆、原始状态可还原；
互斥模型满足对称条件、隐藏领导模型被识别为不满足；安全模型压缩前后均为
`safe` 且压缩后探索状态更少（13 < 20）；缺陷模型压缩前后均为 `violated`；
反例重放与轨迹逐状态一致；预算耗尽时结论为 `pending`；重放拒绝不可执行动作。

## 演示样例输出

```
== 1. Symmetry condition on the declared models ==
mutex (1 leader + 2 workers): condition holds = True
hidden-leader model:          condition holds = False
  witness: shared=() locals=('idle', 'idle') --(0, 'special_request')--> shared=() locals=('waiting', 'idle')
  under permutation (1, 0) the matching transition is missing

== 2. Safe model: reduced vs unreduced exploration ==
symmetry-reduced states explored: 13, verdict=safe
plain states explored:            20, verdict=safe

== 3. Buggy model: counterexample with preserved ID mapping ==
verdict=violated, states explored=16
  [0] shared=(-1,) locals=('idle', 'idle', 'idle')  id_map(orig->canonical)=(0, 1, 2)
  [1] shared=(-1,) locals=('waiting', 'idle', 'idle')  id_map(orig->canonical)=(0, 1, 2)
  [2] shared=(0,) locals=('critical', 'idle', 'idle')  id_map(orig->canonical)=(0, 1, 2)
  [3] shared=(0,) locals=('critical', 'waiting', 'idle')  id_map(orig->canonical)=(0, 2, 1)
  [4] shared=(1,) locals=('critical', 'critical', 'idle')  id_map(orig->canonical)=(0, 1, 2)
  actions: 0:request -> 0:acquire -> 1:request -> 1:acquire
  replay reproduces the trace: True
  final state violates invariant: True

== 4. Unfinished search stays pending ==
budget=5 states explored=5, verdict=pending
```
