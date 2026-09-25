# axcon — 显式轴标签收缩计划器

纯 Python 3.11 标准库实现（不依赖 NumPy/SciPy）的稀疏张量收缩计划器与执行器。
尺寸与索引全部显式输入；执行端使用哈希连接，**从不展开稠密笛卡尔积**
（`10**6 x 10**6` 的维度也能直接跑，见 `tests/test_executor.py`）。

## 功能

- **收缩顺序规划**：枚举全部两两收缩顺序，按中间非零估计（均匀密度假设下的
  哈希连接估计， capped by 结果容量）选择总量最小的计划。
- **显式轴标签**：重复标签即对角线（坐标校验强制对角约束）、单输入独有的标签
  自然广播、标量（rank 0）与空轴（尺寸 0）均受支持。
- **差量与撤回**：`apply_delta(delta_id, entries)` 幂等（同一 id 不会二次应用），
  `retract_delta(delta_id)` 撤回贡献；每次变更递增 `version`。
- **版本绑定缓存**：`SubexpressionCache` 以（子表达式结构, 输入版本）为键；
  `ChunkIndexCache` 把连接索引按坐标分桶，更新一个坐标只失效关联分块
  （未显式失效的版本变化会安全地整体重建）。
- **内存不足处理**：逐块（chunk）处理左操作数，累加器超过
  `max_entries_in_memory` 即溢写（pickle）到工作目录，步末合并。
- **checkpoint 与预算**：每个块边界写 `checkpoint.json` + 累加器快照；
  中断后 `execute(..., resume=True)` 续跑；输入版本变化自动作废 checkpoint；
  块预算耗尽时返回 `completed=False` 并带 `abort_reason`。
- **结果**：`Plan.output` 是 `AxisMapping`（标签→轴位置/尺寸），
  `Plan.stats` 含估计/实际中间规模、块数、溢写字节、是否完成。

## 先验证

构造与计划阶段即抛出 `ValidationError`：非法维度（负数/非整数/布尔）、
标签与形状秩不符、跨输入同名轴尺寸不一致、输出标签未绑定或重复、
坐标越界、违反对角约束。同坐标多贡献相消（和为 0 的项被移除）。

## 布局

- `axcon/tensor.py` — `SparseTensor`：稀疏条目、版本、幂等差量/撤回
- `axcon/planner.py` — 顺序枚举、非零估计、`AxisMapping`/`Plan`/`PlanStep`
- `axcon/executor.py` — 分块哈希连接、溢写、checkpoint、预算中止
- `axcon/cache.py` — 分块索引缓存、子表达式缓存
- `axcon/dense.py` — 小规模稠密枚举 oracle（仅用于核验）
- `samples/replay.py` — 可重放样例（黄金日志 `samples/expected_replay.log`）
- `tests/` — unittest 套件（32 个测试）

## 用法

```python
from axcon import SparseTensor, build_plan, Executor, Budget

a = SparseTensor("A", ("i", "j"), (3, 4), {(0, 0): 1.0, (2, 3): 4.0})
b = SparseTensor("B", ("j", "k"), (4, 2), {(0, 0): 5.0, (3, 1): 8.0})
plan = build_plan([a, b], ("i", "k"))
result = Executor(Budget(chunk_size=256)).execute(plan, [a, b])
assert result.completed
print(result.tensor.entries, plan.output, result.stats)

b.apply_delta("d1", {(1, 1): 2.0})   # 幂等；version + 1
b.retract_delta("d1")                # 撤回
```

## 运行

```sh
python3 -m unittest discover -s tests   # 32 tests
python3 -m samples.replay               # 可重放样例
```

测试结果与样例输出见 `TEST_RESULTS.md`。

## 设计说明 / 限制

- 溢写部分的最终合并在内存中顺序进行（结果本身需能放入内存）；
  生产实现应替换为外部排序归并。预算约束的是连接阶段。
- 非零估计假设均匀密度，仅用于排序比较，不影响正确性。
- checkpoint 以张量名+版本校验；版本不匹配即从头执行。
