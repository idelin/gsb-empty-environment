"""Brute-force reference checker for small domains.

This is the *only* place where states are enumerated, and it exists solely
to cross-validate the symbolic (BDD) results on models whose state space is
small enough.  It evaluates the parsed expressions directly and never uses
the BDD manager, so the two computation paths are independent.
"""

from __future__ import annotations

from . import expr as expr_mod
from .checker import FAIL_BASE, FAIL_STEP, PASS
from .model import load_model


def reference_check(model, invariant=None, max_states=4096, max_pairs=1 << 20):
    """Exhaustively check base case and inductive step by enumeration.

    Returns None when the domain is too large (caller must treat the
    reference verdict as unavailable, never as a failure).
    """
    model = load_model(model)
    n = len(model.variables)
    n_states = 1 << n
    if n_states > max_states:
        return None
    inv_ast = expr_mod.parse(invariant if invariant is not None else model.invariant)

    states = [model.value_to_state(v) for v in range(n_states)]
    init_set = {model.state_value(s) for s in states if model.init_holds(s)}
    inv_set = {model.state_value(s) for s in states if model.invariant_holds(s, inv_ast)}

    # successor relation as adjacency over integer encodings
    kind = model.transition_kind()
    succ = {v: set() for v in range(n_states)}
    if kind in ("edges", "next"):
        for s in states:
            sv = model.state_value(s)
            for t in states:
                if model.transition_holds(s, t):
                    succ[sv].add(model.state_value(t))
    else:  # relation: quadratic enumeration, budgeted
        if n_states * n_states > max_pairs:
            return None
        for s in states:
            sv = model.state_value(s)
            for t in states:
                if model.transition_holds(s, t):
                    succ[sv].add(model.state_value(t))

    base_cex = None
    for v in sorted(init_set):
        if v not in inv_set:
            base_cex = v
            break
    base_ok = base_cex is None

    step_cex = None
    if base_ok:
        for v in sorted(inv_set):
            for w in sorted(succ[v]):
                if w not in inv_set:
                    step_cex = (v, w)
                    break
            if step_cex:
                break
    step_ok = step_cex is None

    # full reachability by BFS over the enumerated graph
    reached = set(init_set)
    frontier = list(init_set)
    while frontier:
        v = frontier.pop()
        for w in succ[v]:
            if w not in reached:
                reached.add(w)
                frontier.append(w)
    holds_on_reachable = all(v in inv_set for v in reached)

    step_cex_reachable = None
    if step_cex is not None:
        step_cex_reachable = step_cex[0] in reached

    if not base_ok:
        verdict = FAIL_BASE
    elif not step_ok:
        verdict = FAIL_STEP
    else:
        verdict = PASS

    return {
        "verdict": verdict,
        "init_empty": len(init_set) == 0,
        "base_ok": base_ok,
        "base_cex": model.value_to_state(base_cex) if base_cex is not None else None,
        "step_ok": step_ok,
        "step_cex": (
            {"state": model.value_to_state(step_cex[0]),
             "next_state": model.value_to_state(step_cex[1])}
            if step_cex else None
        ),
        "step_cex_reachable": step_cex_reachable,
        "reachable_states": len(reached),
        "holds_on_reachable": holds_on_reachable,
        "states_enumerated": n_states,
    }


def cross_check(model, bdd_result, invariant=None, max_states=4096):
    """Compare a symbolic CheckResult against the brute-force reference.

    Returns a list of mismatch strings (empty means agreement).  Unknown /
    resource-limited symbolic verdicts are not cross-checked.
    """
    ref = reference_check(model, invariant=invariant, max_states=max_states)
    if ref is None:
        return ["reference check skipped: domain too large"]
    mismatches = []
    if bdd_result.verdict in (PASS, FAIL_BASE, FAIL_STEP):
        if bdd_result.verdict != ref["verdict"]:
            mismatches.append("verdict: symbolic=%s reference=%s"
                              % (bdd_result.verdict, ref["verdict"]))
    if bdd_result.init_empty != ref["init_empty"]:
        mismatches.append("init_empty: symbolic=%s reference=%s"
                          % (bdd_result.init_empty, ref["init_empty"]))
    if bdd_result.reachable_complete:
        if bdd_result.reachable_states != ref["reachable_states"]:
            mismatches.append("reachable_states: symbolic=%s reference=%s"
                              % (bdd_result.reachable_states, ref["reachable_states"]))
        if bdd_result.holds_on_reachable != ref["holds_on_reachable"]:
            mismatches.append("holds_on_reachable: symbolic=%s reference=%s"
                              % (bdd_result.holds_on_reachable,
                                 ref["holds_on_reachable"]))
    if bdd_result.step_cex is not None and ref["step_cex_reachable"] is not None:
        sym_reach = bdd_result.step_cex.reachability
        if sym_reach in ("reachable", "unreachable"):
            want = "reachable" if ref["step_cex_reachable"] else "unreachable"
            # symbolic cex may differ from reference cex; only compare when
            # the symbolic cex source is a single full state, otherwise skip
            if len(bdd_result.step_cex.state) == len(bdd_result.variable_order) // 2:
                if sym_reach != want:
                    mismatches.append("step_cex reachability: symbolic=%s "
                                      "reference=%s" % (sym_reach, want))
    return mismatches
