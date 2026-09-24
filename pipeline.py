"""变换流水线：逐步执行局部变换，每步记录前后 IR 与结构验证结果；
验证失败则回滚该步，后续步骤继续在当前（未损坏的）IR 上运行。"""
from __future__ import annotations

from dataclasses import dataclass, field

from ir import Instr, validate


@dataclass
class StepReport:
    name: str
    before: list[Instr]
    after: list[Instr]          # 实际生效的 IR（回滚时等于 before）
    candidate: list[Instr]      # 变换产生的候选 IR（无论是否被采纳）
    errors: list[str] = field(default_factory=list)
    applied: bool = False


def run_pipeline(prog: list[Instr],
                 transforms: list[tuple[str, callable]]) -> tuple[list[Instr], list[StepReport]]:
    current = list(prog)
    reports: list[StepReport] = []
    for name, fn in transforms:
        before = list(current)
        candidate = fn(list(current))
        errors = validate(candidate)
        if errors:
            # 结构验证失败：回滚该步，current 保持不变
            reports.append(StepReport(name, before, before, candidate, errors, applied=False))
        else:
            reports.append(StepReport(name, before, candidate, candidate, [], applied=True))
            current = candidate
    return current, reports
