# sr_engine — 可恢复的稀疏半环矩阵引擎

纯 Python 3.11 标准库实现（无 NumPy/SciPy）。面向持续更新的图分析负载：
事务化地更新稀疏矩阵 `A`（m×k）与 `B`（k×n），并增量维护 `C = A × B`，
支持断电恢复、单调版本与一致快照读。

## 半环

三种半环共用一个乘法遍历内核（`Engine._apply`），各自提供零元、幺元与合并运算
（`sr_engine/semirings.py`）：

| 半环 | 零元 | 幺元 | add | mul |
|---|---|---|---|---|
| `boolean` | `False` | `True` | OR | AND |
| `int` | `0` | `1` | `+` | `*` |
| `minplus` | `+inf` | `0` | `min` | `+`（支持正负整数与 `+inf`） |

缺项与显式零语义统一：写入半环零元（`False` / `0` / `+inf`）即删除，存储中
绝不保留零元，矩阵始终保持规范稀疏形式。维度越界、类型错误在提交前校验；
同一事务内内的重复坐标按"后写覆盖"归并。

## 数据结构

- `A`, `B`, `C`: `(i, j) -> value` 稀疏字典。
- 倒排依赖索引：`_A_cols[k] -> {i: v}`（A 的列）、`_B_rows[k] -> {j: v}`（B 的行）。
- 贡献表：`contrib[(i, j)][k] = A[i,k] ⊗ B[k,j]`，按中间下标 `k` 区分每条贡献。

更新 `A[i,k]` 时只需扫描 `_B_rows[k]` 触达的输出 `(i, j)`；更新 `B[k,j]` 时扫描
`_A_cols[k]`。同一事务同时改两边时，先基于旧索引收集受影响输出，再补上
A-op × B-op 的交叉项（`k` 相同的配对），因此结果严格等价于"两个输入同时提交
后重新相乘"，不会漏掉交叉项。聚合时对每个受影响的输出坐标从其贡献映射重新
归约，而不是做差——所以没有减法的半环（min-plus、boolean）也能正确处理删除：

- min-plus 删除最短候选后自动得到次优值；
- boolean 删除一条见证路径后其余路径仍然成立；
- int 贡献抵消到零时输出项消失（规范稀疏），但贡献映射保留，后续更新仍正确。

见证查询 `witnesses(i, j)` 按 `k` 升序返回；min-plus 只返回取得最小值的 `k`
（相同最小值并列时按升序全部给出）。

## 事务与发布点

`Engine.commit(txn_id, ops)` 的流程：

1. 校验全部操作（维度、类型）并归并重复坐标——任何失败都在状态被触碰前抛出，
   事务整体回滚，版本不变。
2. 将记录 `{txn_id, version, ops, opsha, sha}` 追加到 `wal.log` 并 `fsync`。
   **发布点**：记录完整落盘并持久化之后，事务才应用到内存状态并对读者可见。
3. 应用变更、递增单调版本、保存版本快照。

`txn_id` 幂等：相同内容重复提交返回既有版本；不同内容抛出 `DuplicateTxnError`。

## 版本与快照

每次提交产生单调递增版本。`snapshot(version)` 固定任一保留版本，返回 `A`、`B`、
`C` 的不可变一致视图（`MappingProxyType`）。`gc()` 只回收"非当前且未被固定"
的版本；活跃快照释放前其版本不会被回收。并发模型明确为**单写进程、进程内多
读线程**：写路径由互斥锁保护，读者读取不可变快照无需加锁。

## 持久化与恢复协议

数据目录包含：

- `wal.log`：逐行 JSON 事务记录，每条带 SHA-256 校验；
- `checkpoint.json`：全量状态（维度、半环、版本、A、B、事务 ID 表）+ 校验和，
  通过"写临时文件 → fsync → 原子 rename → fsync 目录"安装；
- 检查点安装后轮换日志（截断 `wal.log` 并 fsync）。

恢复（`Engine.open(dir)`）：

1. 读取并校验检查点，全量重建倒排索引与 `C`（检查点恢复允许全量重建）；
2. 顺序扫描日志：尾部截断（撕裂写）的记录被忽略并把文件截断到最后一条有效
   记录边界；中段校验损坏抛出 `CorruptionError`；
3. 跳过版本已被检查点覆盖的记录，逐条重放其余事务——每条记录原子地同时更新
   输入与输出，因此任何恢复出的版本都不会出现"输入已更新而输出未更新"。

故障注入：环境变量 `SR_FAULT_AT`（逗号分隔）在以下持久化边界用 `os._exit`
模拟断电——`log:before_write`、`log:partial_write`（撕裂写）、
`log:after_write_before_fsync`、`log:after_fsync`、`ckpt:before_tmp_write`、
`ckpt:after_tmp_write`、`ckpt:after_rename`、`ckpt:before_log_rotate`、
`ckpt:after_log_rotate`；`SR_FAULT_SKIP=n` 跳过前 n 次触发。

## 复杂度

设事务更新了 `u_A` 个 A 项、`u_B` 个 B 项，`dB(k) = nnz(B 的第 k 行)`，
`dA(k) = nnz(A 的第 k 列)`，`d_ij` 为输出 `(i,j)` 的贡献数：

- 受影响输出收集：`O(Σ_{(i,k)∈u_A} dB(k) + Σ_{(k,j)∈u_B} dA(k) + u_A·u_B)`；
- 每个受影响输出：贡献刷新 `O(1)`/个被触碰的 `k`，聚合 `O(d_ij)`；
- 单坐标更新只重算其倒排依赖触达的输出（由 `engine.stats.contrib_updates` /
  `output_recomputes` 计数器证明，与矩阵其余非零项数量无关）；
- 见证查询 `O(d_ij)`；检查点恢复全量重建 `O(Σ_k dA(k)·dB(k))`；
- 空间：`O(nnz(A) + nnz(B) + Σ d_ij)`，另加每个保留版本一份快照。

## CLI 重放器

```bash
python3.11 -m sr_engine.cli init DIR 2 2 2 minplus
python3.11 -m sr_engine.cli commit DIR t1 '[["set","A",0,0,3],["set","B",0,0,4]]'
python3.11 -m sr_engine.cli checkpoint DIR
python3.11 -m sr_engine.cli show DIR [VERSION]
python3.11 -m sr_engine.cli replay DIR script.json   # [{"txn_id":..,"ops":[..]}, ...]
```

## 库用法

```python
from sr_engine import Engine

eng = Engine.create("db/", m=4, k=5, n=4, semiring="minplus")  # 或 Engine(m,k,n,sr) 纯内存
eng.commit("t1", [["set", "A", 0, 0, 3], ["set", "B", 0, 0, 4]])
eng.commit("t2", [["del", "A", 0, 0]])
with eng.snapshot(1) as snap:      # 固定任一保留版本
    snap.C, snap.value(0, 0), snap.witnesses(0, 0)
eng.checkpoint()
eng = Engine.open("db/")           # 崩溃后恢复
```

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：三种半环 × 随机小矩阵 × 批量双边增删改，逐步与独立稠密三重循环预言机
比较每个保留版本的 `A`、`B`、`C` 与见证；删除并列最优、布尔见证路径存活、
整数抵消归零、维度/类型错误、事务回滚、重复坐标归并、事务 ID 幂等；贡献计数器
确认单坐标更新只触达倒排依赖（50 个无关非零项下仅重算 10 个输出）；崩溃恢复
测试真实重启子进程，在全部 9 个持久化边界注入断电，恢复后逐版本与预言机比较；
尾部截断忽略、中段损坏报错、检查点损坏报错。

实际运行结果（Python 3.11，本仓库提交时）：

```
......................
----------------------------------------------------------------------
Ran 22 tests in 17.872s

OK
```
