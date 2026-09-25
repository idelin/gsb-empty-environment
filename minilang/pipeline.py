"""Transform pipeline with per-step verification and rollback.

For every pass the pipeline records the before/after IR, the structural
validation result and the finite-domain differential check result:
  - structural validation fails  -> roll back this step, continue with next
  - differential check fails     -> roll back this step, continue with next
  - both pass                    -> keep the transformed IR
A broken pass therefore never aborts the pipeline; later safe passes still run.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .diffcheck import DiffReport, check_equivalent
from .ir import IRProgram, format_ir, validate


@dataclass
class StepRecord:
    step: int
    pass_name: str
    before_ir: str
    after_ir: str
    structural_errors: list[str]
    diff_report: DiffReport | None
    kept: bool
    reason: str


@dataclass
class PipelineResult:
    original: IRProgram
    final: IRProgram
    records: list[StepRecord]
    domain: list[tuple[int, ...]]

    @property
    def kept_passes(self) -> list[str]:
        return [r.pass_name for r in self.records if r.kept]

    @property
    def rolled_back_passes(self) -> list[str]:
        return [r.pass_name for r in self.records if not r.kept]


def run_pipeline(
    program: IRProgram,
    transforms: list,
    domain: list[tuple[int, ...]],
    fuel: int = 100_000,
) -> PipelineResult:
    current = program
    records: list[StepRecord] = []
    for step, transform in enumerate(transforms):
        before = current
        after = transform.apply(before)
        before_text = format_ir(before)
        after_text = format_ir(after)

        errors = validate(after)
        if errors:
            records.append(
                StepRecord(
                    step=step,
                    pass_name=transform.name,
                    before_ir=before_text,
                    after_ir=after_text,
                    structural_errors=errors,
                    diff_report=None,
                    kept=False,
                    reason="结构验证失败，回滚该步；继续执行后续优化",
                )
            )
            continue

        report = check_equivalent(before, after, domain, fuel)
        if not report.equivalent:
            records.append(
                StepRecord(
                    step=step,
                    pass_name=transform.name,
                    before_ir=before_text,
                    after_ir=after_text,
                    structural_errors=[],
                    diff_report=report,
                    kept=False,
                    reason="有限域差分测试发现语义差异，回滚该步；继续执行后续优化",
                )
            )
            continue

        current = after
        records.append(
            StepRecord(
                step=step,
                pass_name=transform.name,
                before_ir=before_text,
                after_ir=after_text,
                structural_errors=[],
                diff_report=report,
                kept=True,
                reason="结构验证通过，且有限测试域内行为一致，保留该步",
            )
        )
    return PipelineResult(program, current, records, list(domain))
