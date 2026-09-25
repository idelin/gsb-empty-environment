# 共享规则 DAG 的包含最小事实支持集维护器

纯 Python 3.11 标准库实现（无第三方依赖）。为规则 DAG 上的目标维护
**包含最小（inclusion-minimal）事实支持集**，支持合取、替代（`ANY`）与
显式否定前提（`NOT`），增量修订、按证据成本惰性枚举、预算截断、暂停恢复，
并提供一个**独立验证器**重放并检验每条证据。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试（27 个，含随机穷举）
PYTHONPATH=src python3.11 demo.py             # 端到端可运行样例
```

实际运行结果保存在 `results/unittest_output.txt` 与 `results/demo_output.txt`。

## 目录结构

- `src/supportdag/model.py` —— `Fact` / `Rule` 的稳定 ID、版本；
  合取/`ANY`/`NOT` 归一化为 DNF；JSON 序列化。
- `src/supportdag/semantics.py` —— **独立**语义层：良基模型（WFS，
  Gelfond–Lifshitz 交替不动点）、`entails`、子集格穷举 oracle
  `brute_force_minimal_supports`、最小基数筛选，以及**独立验证器**
  `verify_evidence`（不导入枚举引擎）。
- `src/supportdag/engine.py` —— 惰性枚举引擎：共享原子流 DAG、
  堆式 k-best 成本序笛卡尔积、无根循环剪枝、`Query` 的预算/暂停/恢复。
- `src/supportdag/repository.py` —— `KnowledgeBase`：修订号、
  依赖图反向可达、事实撤回/规则版本变更的**增量**目标修订、目标视图、
  仓库序列化/重放。
- `tests/test_supportdag.py` —— `unittest`。
- `demo.py` —— 8 组合成数据场景。

## 语义要点

- **包含最小 ≠ 最小基数**：`g :- ANY(a, (b,c))` 的最小支持族是
  `{{F_a}, {F_b,F_c}}`，其中只有 `{F_a}` 是最小基数。穷举与引擎对整族
  完全一致；最小基数是其上的进一步筛选。
- **无根循环推不出事实**：`p :- p`、`q :- r; r :- q` 在没有事实落地时
  产生空支持集。判定由 WFS 的数据驱动闭包与引擎的循环剪枝双重保证。
- **否定前提随事实新增失效**：`g :- b, NOT a` 在无 `a` 时支持为 `{F_b}`；
  加入事实 `F_a` 后该支持消失，撤回 `F_a` 后重新出现。否定前提按
  **当前世界**闭包检验；目标充分性/无冗余仍按支持集自包含重放。
- **支配（流式）**：候选按证据成本非递减产出，一旦某集合被已接受的
  严格子集支配即剪枝；因此 `{A}` 支配 `{A,B}`，但绝不支配独立的 `{C}`。
- **共享子证明但不串结果**：多个目标共用同一版本的步骤记录（同一步骤
  稳定 ID），但每个目标持有独立的接受状态与独立枚举游标。

## 惰性、预算与诚实的完成状态

- `GoalView.pull(budget)` 以“检验的原始候选数”为工作量单位。
- 预算用尽返回 `truncated=True, complete=False`；**绝不会在未穷尽时声称
  完整**。可反复 `pull` 暂停/恢复直到 `complete=True`。
- 超预算只返回“未完成（truncated）”，不遗漏后谎称完整。

## 稳定身份、版本与可重放证据

- 事实 ID `fact:<fid>:v<version>`；规则步骤 ID 为
  `rule:<rid>:v<version>:<sha25616(head, 排序后的前提)>`；
  支持 ID 为 `support:<goal>:<sha25616(排序后的 带版本事实引用)>`。
- 事实或规则版本变化会改变对应步骤/支持 ID，并只增量重算依赖目标。
- `Support.to_bundle()` 产出自包含 JSON；`verify_evidence` 独立地：
  1. 用 WFS 检验**充分性**；2. 逐事实删除检验**无冗余**；
  3. 按拓扑序**重放**每条步骤、重算步骤 ID 与支持 ID；
  4. 可选对照当前仓库检测**版本漂移**。篡改任一步骤或加入冗余事实都会
     抛出 `VerificationError`。
