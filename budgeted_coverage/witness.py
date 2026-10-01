"""Solution witnesses and re-validation.

A witness ties a claimed solution to the exact instance it was computed
on (via the instance fingerprint).  After any dynamic change such as
``Instance.remove_set``, the old witness no longer verifies and the
instance must be re-solved; stale results are never silently reused.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import List, Tuple

from .model import Instance, Status


@dataclass(frozen=True)
class Witness:
    fingerprint: str
    selected: Tuple[str, ...]
    claimed_profit: Fraction
    status: Status


@dataclass
class Verification:
    ok: bool
    reasons: List[str] = field(default_factory=list)


def make_witness(instance: Instance, selected, profit, status: Status) -> Witness:
    return Witness(
        instance.fingerprint(),
        tuple(sorted(selected)),
        Fraction(profit),
        status,
    )


def verify_witness(instance: Instance, witness: Witness) -> Verification:
    reasons: List[str] = []
    if witness.fingerprint != instance.fingerprint():
        reasons.append(
            f"instance changed: fingerprint {witness.fingerprint} != "
            f"{instance.fingerprint()}"
        )
    unknown = sorted(set(witness.selected) - set(instance.sets))
    if unknown:
        reasons.append(f"witness selects sets no longer present: {unknown}")
        return Verification(False, reasons)
    ev = instance.evaluate(witness.selected)
    if not ev.feasible:
        reasons.extend(f"infeasible under current instance: {v}" for v in ev.violations)
    if ev.profit != witness.claimed_profit:
        reasons.append(
            f"claimed profit {witness.claimed_profit} != recomputed {ev.profit}"
        )
    return Verification(not reasons, reasons)
