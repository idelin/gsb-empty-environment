"""Certificates: every transformation records the rule used and the domain
on which it is valid, and a claimed bound is *verified* by re-evaluating
the expression with exact (sound) interval arithmetic and checking the
subset relation.  Sampling is never used to prove inclusion -- only to
search for counterexamples (see interval_arith.sampling).
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .interval import Interval


@dataclass
class Step:
    rule: str
    domain: str = "all of Q"


@dataclass
class Certificate:
    claim: str
    steps: list[Step] = field(default_factory=list)

    def add(self, rule: str, domain: str = "all of Q") -> "Certificate":
        self.steps.append(Step(rule, domain))
        return self

    def verify(self, f_interval, domain: Interval, claimed: Interval) -> bool:
        """Re-derive the bound with exact interval arithmetic and check
        f(domain) <= claimed.  Interval evaluation is sound, so a True
        result is a proof of inclusion -- not a sampling artifact."""
        result = f_interval(domain)
        ok = result.subset_of(claimed)
        verdict = "verified" if ok else "VERIFICATION FAILED"
        self.add(
            f"{verdict}: exact interval evaluation gives f({domain}) = {result}, "
            f"subset of claimed {claimed}: {ok}",
            domain=str(domain),
        )
        return ok

    def render(self) -> str:
        lines = [f"claim: {self.claim}", "derivation steps:"]
        for i, s in enumerate(self.steps, 1):
            lines.append(f"  {i}. {s.rule}   [domain: {s.domain}]")
        return "\n".join(lines)
