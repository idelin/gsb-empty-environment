"""Exhaustive orbit verification for small problems.

Enumerates all solutions of the original problem, computes the orbits of
the proven symmetry group, and checks:

1. every retained solution passes the independent checker on the
   original constraints;
2. every orbit keeps at least one retained solution (no class is lost);
3. every deleted solution is recoverable by applying a group element to
   some retained representative.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .core import Problem
from .permutations import apply_perm
from .recovery import assignment_key, recover_symmetric
from .solver import check_original, solve_all


@dataclass
class VerificationReport:
    total_solutions: int = 0
    retained_solutions: int = 0
    num_orbits: int = 0
    orbits_covered: int = 0
    all_retained_valid: bool = False
    all_deleted_recoverable: bool = False
    uncovered_orbits: list = field(default_factory=list)
    unrecoverable: list = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return (
            self.all_retained_valid
            and self.orbits_covered == self.num_orbits
            and self.all_deleted_recoverable
        )

    def summary(self) -> str:
        lines = [
            f"solutions (original): {self.total_solutions}",
            f"solutions (retained): {self.retained_solutions}",
            f"orbits: {self.num_orbits}, covered: {self.orbits_covered}",
            f"retained all valid (independent checker): {self.all_retained_valid}",
            f"deleted all recoverable: {self.all_deleted_recoverable}",
            f"VERDICT: {'OK' if self.ok else 'FAILED'}",
        ]
        if self.uncovered_orbits:
            lines.append(f"uncovered orbits: {self.uncovered_orbits}")
        if self.unrecoverable:
            lines.append(f"unrecoverable solutions: {self.unrecoverable}")
        return "\n".join(lines)


def compute_orbits(solutions, group_elements) -> list:
    """Partition ``solutions`` into orbits under ``group_elements``."""
    by_key = {assignment_key(s): s for s in solutions}
    unseen = set(by_key)
    orbits = []
    while unseen:
        start = unseen.pop()
        orbit = {start}
        frontier = [start]
        while frontier:
            cur = frontier.pop()
            for perm in group_elements:
                img_key = assignment_key(apply_perm(perm, by_key[cur]))
                if img_key in by_key and img_key not in orbit:
                    orbit.add(img_key)
                    frontier.append(img_key)
        unseen -= orbit
        orbits.append(orbit)
    return orbits


def verify_small(problem: Problem, group_elements) -> VerificationReport:
    """Exhaustively verify the symmetry breaking on a small problem."""
    full = solve_all(problem, include_symmetry=False)
    retained = solve_all(problem, include_symmetry=True)

    report = VerificationReport(
        total_solutions=len(full),
        retained_solutions=len(retained),
    )
    report.all_retained_valid = all(check_original(problem, s) for s in retained)

    orbits = compute_orbits(full, group_elements)
    report.num_orbits = len(orbits)
    retained_keys = {assignment_key(s) for s in retained}
    for orbit in orbits:
        if orbit & retained_keys:
            report.orbits_covered += 1
        else:
            report.uncovered_orbits.append(sorted(orbit))

    recovered = {assignment_key(s) for s in recover_symmetric(retained, group_elements)}
    deleted = set(assignment_key(s) for s in full) - retained_keys
    report.unrecoverable = [dict(k) for k in sorted(deleted) if k not in recovered]
    report.all_deleted_recoverable = not report.unrecoverable
    return report
