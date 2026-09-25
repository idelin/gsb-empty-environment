# Evidence-Driven Entity Resolution

纯 Python 3.11+ 标准库实现的实体消解引擎：仅依据**显式等价 / 不等价证据**合并实体，
支持撤回等价边导致分裂，冲突时返回证据环，实体代表 ID 确定可复现。

## 设计要点

- **显式证据驱动**：实体以不透明 key 标识（`e1`、`e2`…），显示名只是属性。
  同名（如两个 "Alice Zhang"）绝不自动合并，只有活跃的 `EQUIV` 事实才会合并分量。
- **撤回即分裂**：每次查询都从活跃证据重算并查集（等价于确定性重放），
  撤回 `EQUIV` 边后分量自然分裂。
- **冲突返回证据环**：若某条 `NEQUIV` 边的两端被一条 `EQUIV` 路径连通
  （如 A=B、B=C 又断言 A≠C），断言被拒绝并抛出 `ConflictError`，
  携带 `EvidenceRing`（等价路径事实 ID + 触发冲突的不等价事实 ID）。
- **代表 ID 确定**：分量的代表 ID 恒为成员中最小的实体 key
  （规则 `rule-representative-min-key`），与断言顺序无关。
- **稳定 ID 与版本**：`Fact`、`Rule`、`Step` 均带稳定 ID 与 `version`；
  每个变更记录为一条 `Step`（`step-0001`…，含引用的规则 ID）。
- **证据可重放**：`Engine.replay(steps)` 仅凭步骤日志重建完全一致的状态。

## 运行

```bash
python3 run_sample.py                  # 合成数据端到端演示
python3 -m unittest discover -s tests -v   # 测试
```

## 样例（合成数据）

`entity_resolution/sample.py` 演示：f1(e1=e2)、f2(e2=e3) 合并后，f3(e1≠e3)
冲突并返回证据环 `f1 -> f2 -> f3`；撤回 f2 后分量分裂，f3 重新断言成功；
同名的 e4 始终保持独立；最后重放 9 个步骤验证状态一致。

```
== assert f3: e1 != e3 (employer records) ==
    CONFLICT rejected: conflict: evidence ring formed by facts f1, f2, f3
    evidence ring: f1 -> f2 -> f3
== retract f2 (phone number was recycled) ==
    components: [e1,e2 rep=e1] [e3 rep=e3] [e4 rep=e4] [e5 rep=e5]
```

## 结构

- `entity_resolution/model.py` — `Fact` / `Rule` / `Step` / `EvidenceRing`（均含 ID + 版本）
- `entity_resolution/rules.py` — 版本化推理规则（传递合并、对称不等价、冲突环、代表 ID）
- `entity_resolution/engine.py` — 引擎：断言、撤回、冲突检测、确定性代表、重放
- `entity_resolution/sample.py` — 合成样例数据与演示
- `tests/test_engine.py` — 17 个 unittest 用例
