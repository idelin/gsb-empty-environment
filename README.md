# BFS 不变量模型检查器（Python 3.11，仅标准库）

对给定有限状态模型做分层同步 BFS，检查不变量并返回**最短**违反轨迹；
预算耗尽时返回 **UNDECIDED** 及覆盖统计与未探索前沿证据。

## 模型接口（`models.py`）

```python
initial()        -> 初始具体状态集合
successors(s)    -> 后继具体状态
is_bad(s)        -> 不变量违反谓词
canonicalize(s)  -> 可哈希规范化形式（可选，默认恒等）
```

## 设计要点

- **最短反例**：按层扩展；去重在发现时**同步**完成、层关闭前生效，
  不使用任何异步/延迟去重，因此首个违反即最小深度（`checker.py`）。
- **状态压缩 + 恢复映射**：`canonicalize` 可合并具体状态（如对称性归约），
  `StateStore.recovery` 保存每个规范形式的首个具体代表，轨迹始终用具体状态、
  可重放（`replay_trace` 逐条转移验证）。
- **哈希碰撞安全**：哈希只用于分桶，同桶用 `==` 比较完整规范状态
  （`store.py`；`CollisionChainModel` 强制所有状态同哈希来验证）。
- **分层磁盘前沿**：内存层超限后溢出到 sqlite 磁盘层，全局 FIFO 保序，
  BFS 层序不受内存限制影响（`frontier.py`）。
- **Checkpoint**：每层结束原子写入（`.tmp` + `os.replace`），包含层号、
  前沿、去重表、恢复映射、父指针与覆盖统计；`run(resume=True)` 从最近的
  层边界继续，不重不漏，覆盖统计跨恢复累计。
- **预算**：`budget_states` / `budget_layers` / `deadline_s`；耗尽即
  UNDECIDED（绝不报 SAFE），附已探索状态数、完成层数、未探索前沿大小
  及前沿证据样本（深度/哈希/状态）。
- **边界语义**：初态违反返回长度 0 轨迹（`trace == [s0]`，0 次转移）；
  不可达坏态不产生反例；未搜索完保持 UNDECIDED。
- **独立对照**：`reference.py` 为不压缩的朴素 BFS，给出可达集与最短距离，
  测试用它交叉验证检查器。

## 运行

```sh
python3.11 run_sample.py      # 运行样例
python3.11 -m unittest test_checker -v   # 测试
```

## 样例输出（节选）

```
=== 1. Shortest counterexample (grid walk to a bad cell) ===
status: violated
trace length (transitions): 5
trace: [(0, 0), (1, 0), (2, 0), (3, 0), (3, 1), (3, 2)]
replayable: True

=== 2. Budget exhausted -> UNDECIDED with frontier evidence ===
status: undecided | reason: state budget exhausted
explored: 50 states, 5 layers | unexplored frontier: 51
  evidence depth=5 hash=50 state=50
  ...

=== 3. Compression with recovery map (token ring symmetry) ===
concrete reachable (reference): 16
canonical stored (compressed): 10

=== 4. Checkpoint / resume across layers ===
resumed: safe | total explored: 127 | reference: 127 | exact: True

=== 5. Initial state violates -> length-0 trace ===
status: violated | trace: [0] | trace length: 0

=== 6. Unreachable bad state is not a counterexample ===
status: safe | explored: 4
```

## 测试结果

```
Ran 17 tests in 0.058s
OK
```
