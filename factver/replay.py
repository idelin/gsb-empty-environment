"""证据重放：用保留的历史版本或步骤自带快照重跑规则并校验结果。"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import FactVersion, FieldValue, StepRecord
from .rules import FactReader, Rule
from .store import FactStore

# 存在性探测为真但从未被读取值的字段，用哨兵占位（其值不影响输出）
_MISSING = object()


@dataclass(frozen=True)
class ReplayReport:
    step_id: str
    version: int
    ok: bool
    checks: dict[str, bool] = field(default_factory=dict)
    detail: str = ""


def _verify(step: StepRecord, rule: Rule, reader: FactReader, output: object) -> ReplayReport:
    checks = {
        "rule_version_matches": rule.version == step.rule_version,
        "output_matches": output == step.output,
        "inputs_match": reader.inputs == step.inputs,
        "existence_match": reader.existence == step.existence,
        "dependencies_match": list(reader.dependencies) == list(step.dependencies),
    }
    bad = [name for name, ok in checks.items() if not ok]
    return ReplayReport(
        step_id=step.step_id,
        version=step.version,
        ok=not bad,
        checks=checks,
        detail="ok" if not bad else "failed: " + ", ".join(bad),
    )


def replay_against_store(store: FactStore, rule: Rule, step: StepRecord) -> ReplayReport:
    """用存储中保留的历史事实版本重放（旧版本必须可获取）。"""
    pinned = step.fact_versions

    def resolver(fact_id: str) -> FactVersion:
        return store.get(fact_id, pinned[fact_id])

    reader = FactReader(resolver)
    try:
        output = rule.derive(reader)
    except Exception as exc:  # 例如被删字段导致旧证据无法重放
        return ReplayReport(step.step_id, step.version, False, detail=f"derive raised {exc!r}")
    return _verify(step, rule, reader, output)


def replay_from_evidence(rule: Rule, step: StepRecord) -> ReplayReport:
    """仅凭步骤记录自带的输入快照重放，证明证据自包含、可独立审计。"""
    pinned = step.fact_versions

    def resolver(fact_id: str) -> FactVersion:
        fields: dict[str, FieldValue] = {}
        for name, present in step.existence.get(fact_id, {}).items():
            if present:
                fields[name] = FieldValue(_MISSING, source="evidence-exists")
        for name, value in step.inputs.get(fact_id, {}).items():
            fields[name] = FieldValue(value, source="evidence")
        return FactVersion(fact_id, pinned[fact_id], fields)

    reader = FactReader(resolver)
    try:
        output = rule.derive(reader)
    except Exception as exc:
        return ReplayReport(step.step_id, step.version, False, detail=f"derive raised {exc!r}")
    return _verify(step, rule, reader, output)
