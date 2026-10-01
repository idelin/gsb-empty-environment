# 请求后最终响应：有限状态活性检查器

在**给定有限状态模型**上检查活性性质 **G(req → F resp)**
（任何未决请求最终都应得到响应），寻找**可达不履行循环**作为反例。
仅使用 Python 3.11 标准库；测试基于 `unittest`。

## 语义约定

- 验证对象仅限显式给出的有限状态模型（`liveness.model.Model`）：
  状态、初始状态、带动作标签的迁移、状态上的原子命题。
- 反例为 lasso 形：`prefix`（有限前缀）+ `cycle`（可无限重复的闭合回路），
  回路上存在 `req` 置位状态且无任何 `resp` 置位状态。
- **公平性只按显式动作集合定义**（`FairnessSet`）：
  - `weak`：集合动作在回路每个状态上持续可执行 ⇒ 回路必须执行该集合动作；
  - `strong`：集合动作在回路某个状态上可执行（无限次经过）⇒ 回路必须执行。
- 判定三值：
  - `violated`：找到可达公平不履行循环（反例可重放）；
  - `satisfied`：状态空间搜索完备且未发现；
  - `pending`：搜索被 `max_states` / `max_depth` 截断且未发现——
    **有限截断内未见到响应不能直接判违反**。
- 自循环（状态经自身迁移回到自身）是合法的非平凡回路，可构成反例。
- 算法：BFS 探索可达子图 → 剔除 `resp` 置位状态 → Tarjan SCC 分解 →
  对含 `req` 的非平凡 SCC 按公平性筛选并缝合见证回路。
- 反例可重放：`liveness.replay.replay` 逐步在模型上重执行 prefix+cycle，
  核验迁移存在性、回路闭合性、req/resp 条件与逐条公平性约束；
  公平假设作为参数传入，假设变化直接体现在重放证据中。

## 运行

```bash
python3.11 -m examples.run_sample   # 运行样例（11 个场景）
python3.11 -m unittest discover -v  # 测试套件
```

## 目录

- `liveness/model.py` — 有限状态模型定义
- `liveness/checker.py` — 活性检查器（探索、SCC、公平性、反例构造）
- `liveness/replay.py` — 反例重放验证器
- `examples/models.py` — 演示模型；`examples/run_sample.py` — 运行样例
- `tests/test_checker.py` — unittest 测试
