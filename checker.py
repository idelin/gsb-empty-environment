"""Orchestration: abstract analysis + bounded concrete replay filtering.

Verdicts:

* ``violated`` - at least one DEFINITE finding (replayable witness found)
* ``verified`` - every abstract candidate was refuted by exhaustive replay
* ``pending``  - the search was incomplete (bound hit) or a candidate
  could be neither confirmed nor refuted: the model stays *undecided*

Evidence levels:

* ``definite`` - a concrete witness trace was found and is replayable
* ``possible`` - the abstract analysis flagged it, but the bounded replay
  could not finish; reported separately from definite leaks
* filtered     - replay exhaustively searched the concrete state graph
  and found no witness: the candidate was a false positive and is dropped
"""

from __future__ import annotations

from dataclasses import dataclass

import abstract as A
import concrete as C
from model import Program

VERIFIED = "verified"
VIOLATED = "violated"
PENDING = "pending"

DEFINITE = "definite"
POSSIBLE = "possible"


@dataclass
class Finding:
    kind: str
    res: str
    node: str
    certainty: str  # DEFINITE | POSSIBLE
    trace: tuple | None  # replayable witness (definite only)


@dataclass
class Report:
    program: str
    verdict: str
    findings: list
    filtered: list  # abstract candidates refuted by exhaustive replay
    pending_reasons: list
    stats: dict


def _witness_pred(prog: Program, cand: A.Candidate):
    node = prog.nodes[cand.node]
    if cand.kind == C.LEAK:
        return lambda cs: cand.res in cs.held_map()
    if cand.kind == C.UNRETURNED_BORROW:
        return lambda cs: any(b[0] == cand.res for b in cs.borrows)
    return lambda cs: C.apply_op(prog, node, cs)[1] == (cand.kind, cand.res)


def check(prog: Program, max_abs_steps: int = 100_000,
          max_replay_states: int = 50_000, max_replay_steps: int = 100_000) -> Report:
    absres = A.run(prog, max_steps=max_abs_steps)
    pending: list[str] = []
    if not absres.complete:
        pending.append(f"abstract fixpoint hit the step bound ({max_abs_steps})")

    findings: list[Finding] = []
    filtered: list[A.Candidate] = []
    seen: set[A.Candidate] = set()
    for cand in absres.candidates:
        if cand in seen:
            continue
        seen.add(cand)
        trace, exhaustive = C.find_witness(
            prog, cand.node, _witness_pred(prog, cand),
            max_states=max_replay_states, max_steps=max_replay_steps,
        )
        if trace is not None:
            findings.append(Finding(cand.kind, cand.res, cand.node, DEFINITE, trace))
        elif exhaustive:
            filtered.append(cand)  # false positive, refuted by replay
        else:
            findings.append(Finding(cand.kind, cand.res, cand.node, POSSIBLE, None))
            pending.append(
                f"replay bound reached before resolving {cand.kind} "
                f"of '{cand.res}' at '{cand.node}'"
            )

    if any(f.certainty == DEFINITE for f in findings):
        verdict = VIOLATED
    elif pending:
        verdict = PENDING
    else:
        verdict = VERIFIED
    return Report(
        prog.name,
        verdict,
        findings,
        filtered,
        pending,
        {"abstract_steps": absres.steps, "abstract_states": len(absres.states)},
    )


def soundness_check(prog: Program, max_states: int = 100_000,
                    max_steps: int = 200_000):
    """Reliability cross-check for small programs: enumerate the concrete
    state graph exhaustively and require that every concrete violation was
    flagged by the abstract analysis (no false negatives).

    Returns ``(missing, complete)``; ``missing`` must be empty.
    """
    absres = A.run(prog)
    flagged = {(c.kind, c.res, c.node) for c in absres.candidates}
    violations, complete, _ = C.explore(prog, max_states=max_states, max_steps=max_steps)
    missing = sorted({(v.kind, v.res, v.node) for v in violations} - flagged)
    return missing, complete and absres.complete
