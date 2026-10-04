# depsys — 字段级溯源与失效引擎（Python 3.11，仅标准库）

合成数据样例：薪资流水线。事实 / 规则 / 步骤均带稳定 ID 与整数版本。

## 运行

```bash
python3.11 -m depsys.sample          # 运行样例（7 个演示小节）
python3.11 -m unittest discover -s tests -v   # 22 个测试
```

## 需求映射

| 需求 | 实现 |
| --- | --- |
| 步骤读输入版本、产出版本 | `Engine.register_step` / `_run_step`，产物字段变化才 bump 版本 |
| 查询输入修订影响哪些下游产物 | `Engine.query_impact` → `ImpactReport.products` |
| 区分直接 / 传递依赖、最短链 | BFS 标记，`Explanation.distance`（1=直接，>1=传递）， hops 即最短链 |
| 依赖记录区分读字段 / 条件分支 / 负依赖 | `DepKind.READ / BRANCH / NEGATIVE`，经 `ReadContext` 记录 |
| 新增原本不存在的输入触发失效 | `FactAdd` 命中 NEGATIVE 边 |
| 步骤更正撤回旧边 | `StepCorrection`：重跑前整体撤回该步骤旧边，再记录新边 |
| 循环失效不能自我支撑 | BFS 访问集保证简单路径；`CycleError` 诊断环链并精确回滚 |
| 批量变更：最小解释路径 + 重验证后不再受影响节点 | `BatchReport.explanations`（最短）与 `rescued`（拓扑序重验证，产物未变则下游获救） |
| 完整重执行参考检查：无漏失效 + 无关分支访问数 | `Engine.reference_check` → `ReferenceReport.missed` 与 `irrelevant_branch_accesses` |
| 事实 / 规则 / 步骤稳定 ID + 版本 | `Fact.version`、`Step.version/rule_version`、`Engine.rules` |
| 证据可重放 | `Explanation.replay(engine)`，对照当前边与只追加日志 `journal` 重新验证 |
| 旧版本消费者不自动依赖新版本 | 失效按“变更字段 ∩ 已消费字段”判定，与版本号无关 |
| 未消费变更字段的步骤可不受影响 | 同上；`rate_g6` 变更不影响只读 `rate_g5` 的步骤 |
| 环状非法处理链须诊断 | `_find_cycle` 返回完整环链并抛 `CycleError` |

## 结构

- `depsys/model.py` — Fact / Step / DepEdge / 变更描述（FactUpdate、FactAdd、RuleUpdate、StepCorrection）
- `depsys/engine.py` — Engine：记录、影响查询、批量失效（标记 + 重验证两阶段）、环诊断、参考检查
- `depsys/sample.py` — 合成薪资流水线样例（`python3.11 -m depsys.sample`）
- `tests/test_engine.py` — 按需求逐条对应的 unittest
