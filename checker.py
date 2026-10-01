"""Independent trace replay checker.

Re-executes the event trace from the ORIGINAL CSP and verifies that every
event is supported by the proof rules:

  decide     value must be in the current domain of a non-singleton var;
             one branch variable per level; no value tried twice
  propagate  the deleted value must currently be in the domain AND must
             have no support in the cited constraint (unjustified domain
             deletions are rejected)
  conflict   the cited domain must actually be empty
  backtrack  must pop exactly one checkpoint; the restored state must
             equal the snapshot recorded in the event
  exhausted  every value of the branch variable at this level must have
             been tried and failed
  solution   domains must all be singletons matching the claim, and the
             assignment is re-validated against the ORIGINAL constraints
             by the independent solution checker
  unsat      only after the root level was exhausted (complete search) or
             a conflict was derived at the root; an inconclusive trace can
             never be certified as UNSAT
  unknown    terminal, certifies nothing about satisfiability

On failure the checker reports the index of the FIRST illegal event.
"""

from __future__ import annotations

from dataclasses import dataclass

from csp import CSP, check_solution, has_support
from solver import SAT, UNKNOWN, UNSAT
from trace import Event


@dataclass
class CheckResult:
    ok: bool
    status: str | None        # certified status, only meaningful if ok
    first_bad: int | None     # index of first illegal event, if any
    message: str


def check_trace(csp: CSP, trace: list[Event]) -> CheckResult:
    domains: dict[str, set] = {v: set(d) for v, d in csp.domains.items()}
    checkpoints: list[dict[str, set]] = []
    tried: dict[int, dict] = {}   # level -> {var, domain, values}
    root_failed = False
    status: str | None = None

    def bad(i: int, msg: str) -> CheckResult:
        return CheckResult(False, None, i, msg)

    for i, ev in enumerate(trace):
        if ev.seq != i:
            return bad(i, f"sequence number {ev.seq} != position {i}")
        if status is not None:
            return bad(i, f"event after terminal {status} event")
        kind, p = ev.kind, ev.payload
        if kind != "unsat":
            root_failed = False

        if kind == "decide":
            var, value = p["var"], p["value"]
            if var not in domains:
                return bad(i, f"decide on unknown variable {var!r}")
            if len(domains[var]) < 2:
                return bad(i, f"decide on singleton/empty domain of {var!r}")
            if value not in domains[var]:
                return bad(i, f"decide {var}={value!r} not in current domain")
            level = len(checkpoints)
            rec = tried.get(level)
            if rec is None:
                rec = tried[level] = {
                    "var": var, "domain": set(domains[var]), "values": set(),
                }
            elif rec["var"] != var:
                return bad(i, f"level {level} branches on both "
                              f"{rec['var']!r} and {var!r}")
            if value in rec["values"]:
                return bad(i, f"value {value!r} of {var!r} tried twice")
            rec["values"].add(value)
            checkpoints.append({v: set(d) for v, d in domains.items()})
            domains[var] = {value}

        elif kind == "propagate":
            var, value, ci = p["var"], p["value"], p["constraint"]
            if var not in domains:
                return bad(i, f"propagate on unknown variable {var!r}")
            if not (0 <= ci < len(csp.constraints)):
                return bad(i, f"no constraint with index {ci}")
            con = csp.constraints[ci]
            if var not in con.scope:
                return bad(i, f"constraint #{ci} does not mention {var!r}")
            if value not in domains[var]:
                return bad(i, f"{var}={value!r} already absent from domain")
            if has_support(con, domains, var, value):
                return bad(i, f"unjustified deletion: {var}={value!r} still "
                              f"has support in constraint #{ci}")
            domains[var].discard(value)

        elif kind == "conflict":
            var = p["var"]
            if var not in domains:
                return bad(i, f"conflict on unknown variable {var!r}")
            if domains[var]:
                return bad(i, f"conflict claimed but domain of {var!r} "
                              f"is {sorted(domains[var])}")
            if not checkpoints:
                root_failed = True

        elif kind == "backtrack":
            level = p["level"]
            if level != len(checkpoints) - 1:
                return bad(i, f"backtrack to level {level} but checkpoint "
                              f"depth is {len(checkpoints)}")
            restored = checkpoints.pop()
            domains = restored
            claimed = {v: set(vals) for v, vals in p["domains"].items()}
            if claimed != domains:
                return bad(i, "restored snapshot does not match the state "
                              "recorded in the backtrack event")
            for lv in [lv for lv in tried if lv > level]:
                del tried[lv]

        elif kind == "exhausted":
            var = p["var"]
            level = len(checkpoints)
            rec = tried.get(level)
            if rec is None or rec["var"] != var:
                return bad(i, f"exhausted {var!r} but level {level} did not "
                              f"branch on it")
            if rec["values"] != rec["domain"]:
                return bad(i, f"exhausted {var!r} but only "
                              f"{sorted(rec['values'])} of "
                              f"{sorted(rec['domain'])} were tried")
            if level == 0:
                root_failed = True

        elif kind == "solution":
            assignment = p["assignment"]
            if any(len(d) != 1 for d in domains.values()):
                return bad(i, "solution claimed with non-singleton domains")
            current = {v: next(iter(d)) for v, d in domains.items()}
            if assignment != current:
                return bad(i, "solution does not match the replayed state")
            ok, msg = check_solution(csp, assignment)
            if not ok:
                return bad(i, f"independent solution check failed: {msg}")
            status = SAT

        elif kind == "unsat":
            if checkpoints:
                return bad(i, "unsat claimed with open checkpoints")
            if not root_failed:
                return bad(i, "unsat without an exhausted/conflicting root "
                              "level (search was not complete)")
            status = UNSAT

        elif kind == "unknown":
            status = UNKNOWN  # trace may be valid, but proves nothing

        else:
            return bad(i, f"unknown event kind {kind!r}")

    if status is None:
        return CheckResult(False, None, None, "trace has no terminal event")
    return CheckResult(True, status, None, "trace verified")
