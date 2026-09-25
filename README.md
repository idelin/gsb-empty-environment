# factver：版本化事实 + 字段级依赖追踪的可重放推导引擎

Python 3.11，仅标准库。演示“事实部分字段更正”场景下的精确推导失效与证据重放。

## 能力

- **部分字段更正**：`Correction` 支持修改/新增字段（带来源）与删除字段；旧版本全部保留，未触及字段保留原值与原来源（`FactStore` / `FieldValue.source`）。
- **字段级失效**：规则通过 `FactReader` 读取，实际读取被记录为依赖（`value` / `exists` / `wildcard`）。只有实际读取了变更字段的推导步骤失效；通配读取（`snapshot`）视为依赖全部字段。
- **稳定 ID 与版本**：事实（`fact_id`+`version`）、规则（`rule_id`+`version`）、步骤（`step_id`+`version`）均带稳定标识，历史版本可审计。
- **语义示例**：改备注不影响仅读数值的规则；删除字段使存在性条件失效；批量更正原子生效——任一失败则整体保持原版本。
- **可重放证据**：每个 `StepRecord` 记录规则版本、依赖、输入快照与存在性探测；`replay_against_store`（基于保留的历史版本）与 `replay_from_evidence`（仅凭步骤记录，自包含）两种方式重放校验。

## 运行

```bash
python3 sample.py                      # 合成数据演示
python3 -m unittest discover -s tests  # 15 个测试
```

## 结构

- `factver/model.py` — 事实版本、字段来源、依赖、更正、步骤记录、字段差异
- `factver/store.py` — 版本化存储，原子批量更正
- `factver/rules.py` — `FactReader`（读取追踪）与 `Rule`
- `factver/engine.py` — 规则执行、步骤记录、字段级失效判定
- `factver/replay.py` — 证据重放与校验
- `sample.py` / `tests/test_factver.py` — 样例与测试
