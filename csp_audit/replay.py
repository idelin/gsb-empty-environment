"""Independent replay checker.

Re-executes a trail event by event against a FRESH copy of the problem and
verifies that every step is supported by a rule:

  DECIDE(var, value, level)
      - level must equal the current decision depth
      - value must currently be in var's domain
  PROPAGATE(var, value, cid, level)
      - cid must name a real constraint whose scope contains var
      - value must currently be in var's domain
      - value must have NO support in that constraint under the current
        domains (otherwise the deletion is unjustified -> illegal)
  CONFLICT(var, cid, level)
      - var's domain must actually be empty right now
  BACKTRACK(var, value, level)
      - must match the innermost open decision exactly
      - that decision's subtree must carry failure evidence: either a
        verified CONFLICT occurred inside it, or every value of the
        variable chosen at the next level was tried and backtracked
      - the checker restores ITS OWN snapshot and removes the tried value

Final verdicts:
  SAT     requires every domain to be a singleton at the end of the trail
          (a complete assignment); the induced assignment is re-validated
          by verify.check_sat against the original constraints.
  UNSAT   requires zero open decisions and (a root conflict OR some domain
          empty at root state).
  UNKNOWN makes no claim; only event legality is checked.

Any violation reports the index of the FIRST illegal event.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import Problem
from .trail import BACKTRACK, CONFLICT, DECIDE, PROPAGATE, KINDS, Trail
from .verify import check_sat


@dataclass
class ReplayResult:
    ok: bool
    first_bad_index: int | None = None
    message: str = ""
    verdict_supported: bool = False


@dataclass
class _Frame:
    var: str
    value: object
    level: int
    domain_at_decide: set
    conflict: bool = False
    child_var: str | None = None
    child_domain: set = field(default_factory=set)
    child_tried: set = field(default_factory=set)

    @property
    def child_exhausted(self) -> bool:
        return (
            self.child_var is not None
            and bool(self.child_domain)
            and self.child_tried >= self.child_domain
        )


def replay(problem: Problem, trail: Trail, claimed_status: str,
           claimed_assignment: dict | None = None) -> ReplayResult:
    domains = {v: set(d) for v, d in problem.domains.items()}
    snapshots: dict[int, dict[str, set]] = {}
    frames: list[_Frame] = []
    root_conflict = False

    def bad(i: int, msg: str) -> ReplayResult:
        return ReplayResult(False, i, msg, False)

    for i, ev in enumerate(trail):
        if ev.kind not in KINDS:
            return bad(i, f"unknown event kind {ev.kind!r}")

        if ev.kind == DECIDE:
            if ev.level != len(frames):
                return bad(i, f"DECIDE level {ev.level} != depth {len(frames)}")
            if ev.var not in domains:
                return bad(i, f"DECIDE on unknown variable {ev.var!r}")
            if ev.value not in domains[ev.var]:
                return bad(i, f"DECIDE {ev.var}={ev.value}: value not in current domain")
            snapshots[ev.level] = {v: set(d) for v, d in domains.items()}
            frames.append(_Frame(ev.var, ev.value, ev.level, set(domains[ev.var])))
            domains[ev.var] = {ev.value}

        elif ev.kind == PROPAGATE:
            if ev.level != len(frames):
                return bad(i, f"PROPAGATE level {ev.level} != depth {len(frames)}")
            con = problem.constraint(ev.cid) if ev.cid else None
            if con is None:
                return bad(i, f"PROPAGATE cites unknown constraint {ev.cid!r}")
            if ev.var not in con.scope:
                return bad(i, f"PROPAGATE: {ev.var} not in scope of {ev.cid}")
            if ev.value not in domains[ev.var]:
                return bad(i, f"PROPAGATE removes {ev.value} not in domain of {ev.var}")
            if con.has_support(ev.var, ev.value, domains):
                return bad(i, f"unjustified deletion: {ev.var}={ev.value} still has "
                              f"support in {ev.cid}")
            domains[ev.var].discard(ev.value)

        elif ev.kind == CONFLICT:
            if ev.level != len(frames):
                return bad(i, f"CONFLICT level {ev.level} != depth {len(frames)}")
            if ev.var not in domains:
                return bad(i, f"CONFLICT on unknown variable {ev.var!r}")
            if domains[ev.var]:
                return bad(i, f"CONFLICT claimed on {ev.var} but domain is non-empty")
            if frames:
                frames[-1].conflict = True
            else:
                root_conflict = True

        elif ev.kind == BACKTRACK:
            if not frames:
                return bad(i, "BACKTRACK with no open decision")
            if ev.level != len(frames) - 1:
                return bad(i, f"BACKTRACK level {ev.level} != depth-1 {len(frames)-1}")
            top = frames[-1]
            if (top.var, top.value, top.level) != (ev.var, ev.value, ev.level):
                return bad(i, f"BACKTRACK {ev.var}={ev.value} does not match open "
                              f"decision {top.var}={top.value}")
            if not (top.conflict or top.child_exhausted):
                return bad(i, f"BACKTRACK of {ev.var}={ev.value} has no verified "
                              f"failure evidence (no conflict, child not exhausted)")
            frames.pop()
            domains = {v: set(d) for v, d in snapshots[top.level].items()}
            for deeper in [l for l in snapshots if l > top.level]:
                del snapshots[deeper]
            domains[top.var].discard(top.value)
            if frames:
                parent = frames[-1]
                if parent.child_var != top.var:
                    parent.child_var = top.var
                    parent.child_domain = set(top.domain_at_decide)
                    parent.child_tried = set()
                parent.child_tried.add(top.value)

    # -- final verdict ------------------------------------------------------
    status = claimed_status.upper() if isinstance(claimed_status, str) else None
    if status == "SAT":
        # Open frames are the successful decision path; what matters is that
        # the leaf state assigns every variable exactly one value.
        if any(len(d) != 1 for d in domains.values()):
            return bad(len(trail) - 1, "SAT claimed but assignment is not complete")
        assignment = {v: next(iter(d)) for v, d in domains.items()}
        if claimed_assignment is not None and claimed_assignment != assignment:
            return bad(len(trail) - 1, "claimed assignment does not match trail state")
        if not check_sat(problem, assignment):
            return bad(len(trail) - 1, "assignment violates original constraints")
        return ReplayResult(True, None, "SAT verified against original constraints", True)
    if status == "UNSAT":
        if frames:
            return bad(len(trail) - 1, "UNSAT claimed with open decisions")
        if not (root_conflict or any(len(d) == 0 for d in domains.values())):
            return bad(len(trail) - 1, "UNSAT not supported: no root conflict and "
                                       "no exhausted root domain")
        return ReplayResult(True, None, "UNSAT supported by trail", True)
    if status == "UNKNOWN":
        return ReplayResult(True, None, "no verdict claimed; events legal", False)
    return bad(len(trail) - 1, f"unrecognized claimed status {claimed_status!r}")
