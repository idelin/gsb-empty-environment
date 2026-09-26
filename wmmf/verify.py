"""综合核验报告：可行、最优、预算未决三者严格分开。"""

from __future__ import annotations

from dataclasses import dataclass, field

from .allocator import Allocation
from .certificate import build_certificate, check_feasible, verify_certificate
from .exhaustive import GridResult, grid_verify


@dataclass
class VerificationReport:
    feasible_errors: list[str]
    certificate: dict[str, str] | None
    certificate_errors: list[str]
    grid: GridResult | None = None

    @property
    def feasible(self) -> bool:
        return not self.feasible_errors

    @property
    def optimal(self) -> bool:
        return not self.certificate_errors


def verify_all(allocation: Allocation, grid_step=None,
               grid_budget: int = 100_000) -> VerificationReport:
    """对分配结果做可行性 + 证书最优性核验，可选做网格穷举核验。"""
    feasible_errors = check_feasible(allocation)
    cert = None
    cert_errors: list[str] = []
    if not feasible_errors:
        try:
            cert = build_certificate(allocation)
            cert_errors = verify_certificate(allocation, cert)
        except ValueError as exc:
            cert_errors = [str(exc)]
    grid = None
    if grid_step is not None:
        grid = grid_verify(
            allocation.link_caps, allocation.demands, allocation.rates,
            grid_step, grid_budget,
        )
    return VerificationReport(feasible_errors, cert, cert_errors, grid)
