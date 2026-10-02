"""Brute-force reference checker: full state enumeration.

Used ONLY as an independent cross-check for small domains (tests and
the explicit ``--reference`` / ``enumerate`` paths).  The symbolic
checker never relies on it.  If the state space exceeds ``max_states``
the reference check stays ``undecided`` rather than guessing.
"""

from __future__ import annotations

from itertools import product

from . import expr as exprmod
from .model import next_states


def all_states(variables):
    for bits in product((False, True), repeat=len(variables)):
        yield dict(zip(variables, bits))


def enumerate_check(model, inv_ast, max_states=1 << 16):
    n = len(model.variables)
    total = 1 << n
    if total > max_states:
        return {
            "status": "undecided",
            "reason": f"state space 2^{n} = {total} exceeds enumeration limit {max_states}",
        }
    states = list(all_states(model.variables))
    key = lambda s: tuple(bool(s[v]) for v in model.variables)
    unkey = lambda k: dict(zip(model.variables, k))

    init_keys = [key(s) for s in states if exprmod.eval_ast(model.init, s)]
    inv_keys = {key(s) for s in states if exprmod.eval_ast(inv_ast, s)}
    succ = {key(s): [key(t) for t in next_states(model, s)] for s in states}

    base_status = "empty_init" if not init_keys else "pass"
    base_cex = None
    for k in init_keys:
        if k not in inv_keys:
            base_status = "fail"
            base_cex = unkey(k)
            break

    ind_status = "pass"
    ind_cex = None
    for s in states:
        sk = key(s)
        if sk not in inv_keys:
            continue
        for tk in succ[sk]:
            if tk not in inv_keys:
                ind_status = "fail"
                ind_cex = (unkey(sk), unkey(tk))
                break
        if ind_cex is not None:
            break

    reach = set(init_keys)
    frontier = list(init_keys)
    while frontier:
        k = frontier.pop()
        for tk in succ[k]:
            if tk not in reach:
                reach.add(tk)
                frontier.append(tk)
    holds_on_reachable = all(k in inv_keys for k in reach)

    if base_status == "fail":
        verdict = "base_case_failed"
    elif ind_status == "fail":
        verdict = "inductive_failed"
    else:
        verdict = "verified"

    return {
        "status": "decided",
        "verdict": verdict,
        "base_status": base_status,
        "inductive_status": ind_status,
        "base_counterexample": base_cex,
        "inductive_counterexample": ind_cex,
        "reachable_count": len(reach),
        "holds_on_reachable": holds_on_reachable,
    }


def compare(symbolic, enum):
    """Mismatches between a symbolic CheckResult and an enumeration dict."""
    if enum.get("status") != "decided":
        return ["reference enumeration is undecided"]
    mismatches = []
    for field in ("verdict", "base_status", "inductive_status"):
        if getattr(symbolic, field) != enum[field]:
            mismatches.append(
                f"{field}: symbolic={getattr(symbolic, field)} enum={enum[field]}"
            )
    if symbolic.reachable_checked:
        if symbolic.reachable_count != enum["reachable_count"]:
            mismatches.append(
                f"reachable_count: symbolic={symbolic.reachable_count} "
                f"enum={enum['reachable_count']}"
            )
        if symbolic.holds_on_reachable != enum["holds_on_reachable"]:
            mismatches.append(
                f"holds_on_reachable: symbolic={symbolic.holds_on_reachable} "
                f"enum={enum['holds_on_reachable']}"
            )
    return mismatches
