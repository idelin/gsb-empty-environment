"""Differential checker: runs two IR programs on a *finite* test domain and
compares observable behaviour (outputs, final variables, trap state).

IMPORTANT: a positive result means 'equivalent on the tested domain only'.
It is NOT a proof of semantic equivalence over the whole input space.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .interp import RunResult, run
from .ir import IRProgram

DISCLAIMER = (
    "注意：差分验证仅在上述有限测试域内成立，"
    "不构成对全部输入空间的语义等价证明。"
)


@dataclass
class Mismatch:
    inputs: tuple[int, ...]
    before: RunResult
    after: RunResult


@dataclass
class DiffReport:
    domain: list[tuple[int, ...]]
    mismatches: list[Mismatch] = field(default_factory=list)

    @property
    def equivalent(self) -> bool:
        return not self.mismatches

    @property
    def domain_size(self) -> int:
        return len(self.domain)

    def summary(self) -> str:
        status = "一致" if self.equivalent else f"发现 {len(self.mismatches)} 处差异"
        return (
            f"有限测试域共 {self.domain_size} 个输入，结果：{status}。"
            + ("" if self.equivalent else "（详见 mismatches）")
            + DISCLAIMER
        )


def check_equivalent(
    before: IRProgram,
    after: IRProgram,
    domain: list[tuple[int, ...]],
    fuel: int = 100_000,
) -> DiffReport:
    report = DiffReport(domain=list(domain))
    for inputs in report.domain:
        rb = run(before, inputs, fuel)
        ra = run(after, inputs, fuel)
        if rb.observable() != ra.observable():
            report.mismatches.append(Mismatch(inputs, rb, ra))
    return report
