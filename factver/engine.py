"""推导引擎：执行规则、记录步骤、按字段级依赖精确失效。"""
from __future__ import annotations

from .model import (
    DEP_EXISTS,
    DEP_VALUE,
    DEP_WILDCARD,
    STATUS_INVALID,
    STATUS_VALID,
    STATUS_SUPERSEDED,
    Correction,
    FieldDiff,
    StepRecord,
    diff_fields,
)
from .rules import FactReader, Rule
from .store import FactStore


class Engine:
    def __init__(self, store: FactStore) -> None:
        self.store = store
        self.rules: dict[str, Rule] = {}
        self.steps: dict[str, list[StepRecord]] = {}
        self._counter = 0

    def register(self, rule: Rule) -> None:
        self.rules[rule.rule_id] = rule

    def run(self, rule_id: str, step_id: str | None = None) -> StepRecord:
        """对当前事实版本执行规则，产生步骤的新版本。"""
        rule = self.rules[rule_id]
        reader = FactReader(self.store.get)
        output = rule.derive(reader)
        if step_id is None:
            self._counter += 1
            step_id = f"step-{self._counter:04d}"
        history = self.steps.setdefault(step_id, [])
        for prev in history:
            if prev.status == STATUS_VALID:
                prev.status = STATUS_SUPERSEDED
        rec = StepRecord(
            step_id=step_id,
            version=len(history) + 1,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            dependencies=tuple(reader.dependencies),
            inputs={k: dict(v) for k, v in reader.inputs.items()},
            existence={k: dict(v) for k, v in reader.existence.items()},
            output=output,
        )
        history.append(rec)
        return rec

    def apply_corrections(self, corrections: list[Correction]) -> list[StepRecord]:
        """原子应用批量更正，并只失效实际读取了变更字段的步骤。"""
        olds = {c.fact_id: self.store.get(c.fact_id) for c in corrections}
        news = self.store.batch_correct(corrections)  # 失败则抛错，步骤不受影响
        diffs = {nv.fact_id: diff_fields(olds[nv.fact_id], nv) for nv in news}
        invalidated: list[StepRecord] = []
        for rec in self.latest_steps():
            if rec.status != STATUS_VALID:
                continue
            reason = self._invalidation_reason(rec, diffs)
            if reason is not None:
                rec.status = STATUS_INVALID
                rec.invalid_reason = reason
                invalidated.append(rec)
        return invalidated

    def latest_steps(self) -> list[StepRecord]:
        return [hist[-1] for hist in self.steps.values()]

    def all_steps(self) -> list[StepRecord]:
        return [rec for hist in self.steps.values() for rec in hist]

    @staticmethod
    def _invalidation_reason(rec: StepRecord, diffs: dict[str, FieldDiff]) -> str | None:
        for dep in rec.dependencies:
            diff = diffs.get(dep.fact_id)
            if diff is None or not diff.any_change:
                continue
            if dep.kind == DEP_WILDCARD:
                return f"wildcard read of {dep.fact_id}@v{dep.fact_version} changed"
            if dep.kind == DEP_VALUE and (
                dep.field in diff.value_changed or dep.field in diff.removed
            ):
                return f"value of {dep.fact_id}.{dep.field} changed/removed"
            if dep.kind == DEP_EXISTS and (
                dep.field in diff.added or dep.field in diff.removed
            ):
                return f"existence of {dep.fact_id}.{dep.field} changed"
        return None
