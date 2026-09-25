# axcon — 显式轴标签收缩计划器

纯 Python 3.11+ 标准库实现（不依赖 NumPy/SciPy）的 einsum 式收缩计划与执行器。
尺寸与索引全部显式输入；小规模正确性由自写稠密枚举 oracle 核验。

## 功能

- **顺序规划**：枚举二叉收缩树，以中间张量非零估计（维度乘积上界，绝不展开
  笛卡尔积）最小化为目标选择顺序；三输入时 `(AB)C` 与 `A(BC)` 结果一致但
  中间规模不同，规划器自动选小。
- **语义**：重复标签对角提取、广播（单侧标签直传输出）、标量、空轴（尺寸 0）、
  同坐标多贡献相消（零值自动清除）。
- **分块存储**：`SparseTensor` 按块存储稀疏条目；单坐标 delta 只失效所在块
  （`dirty_chunks`），并 bump 版本。
- **版本绑定缓存**：`ContractionCache` 以血统戳（输入名+版本 / 上游步骤键）为键，
  delta 后下游子表达式结构性失效。
- **内存不足**：输出缓冲超过 `Budget.buffer_entries` 即溢写 JSON run 文件，
  最后归并；每处理完一块写 checkpoint，崩溃续跑不重复应用任何贡献；
  delta 带 `delta_id`，重复应用是幂等 no-op。
- **预算中止**：贡献数超过 `Budget.max_contributions` 抛 `BudgetExceeded`，
  `PlanResult.complete=False` 并携带部分统计。
- **先验证**：负维度、越界坐标、共享标签尺寸不一致、未知/重复输出标签等
  一律 `ValidationError`。

## 结果内容

`contract_many` 返回 `PlanResult`：结果张量、`axis_mapping`（输出标签→轴位）、
`plan`（步骤与估计）、`stats`（估计/实际中间非零、处理块数、溢写文件数、
缓存命中、续跑步数等）、`complete` 标志。

## 运行

```bash
python3 -m unittest discover -s tests -v   # 26 个测试
python3 examples/replay_demo.py            # 可重放样例（确定性输出）
```

## 布局

- `axcon/tensor.py` — 分块稀疏张量、幂等 `Delta`
- `axcon/planner.py` — 收缩顺序枚举与中间非零估计
- `axcon/engine.py` — 逐对收缩：对角、广播、分块、溢写、checkpoint、预算
- `axcon/cache.py` / `axcon/api.py` — 版本绑定缓存与高层编排
- `axcon/dense.py` — 稠密枚举 oracle（仅小规模）
- `examples/replay_demo.py` — 可重放场景：规划→缓存→delta→溢写崩溃续跑→预算中止
