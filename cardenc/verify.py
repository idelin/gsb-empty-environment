"""Projection-equivalence verification and propagation-strength analysis."""

from __future__ import annotations

from itertools import combinations, islice, product

from .core import solve, unit_propagate
from .encodings import ATMOST, ATLEAST, EXACTLY, check_direct, encode


def exists_aux_extension(enc, values, enum_limit=12):
    """Decide whether the CNF has a model extending the original assignment.

    For small auxiliary spaces every auxiliary assignment is enumerated
    exhaustively; otherwise the built-in DPLL solver is used.
    """
    if enc.cnf.is_trivially_unsat():
        return False
    assumptions = [lit if values[abs(lit)] else -lit for lit in enc.lits]
    if len(enc.aux) <= enum_limit:
        base = {abs(lit): values[abs(lit)] for lit in enc.lits}
        for bits in product((False, True), repeat=len(enc.aux)):
            assign = dict(base)
            assign.update(zip(enc.aux, bits))
            if enc.cnf.evaluate(assign) is True:
                return True
        return False
    return solve(enc.cnf, assumptions=assumptions) is not None


def verify_projection_equivalence(pool, n, op, k, method="seq", enum_limit=12):
    """Exhaustively check: original assignment satisfies the constraint iff
    some auxiliary assignment satisfies the CNF (both directions).

    Also validates the ``lift`` witness for every satisfying assignment.
    """
    lits = pool.reserve(n, "x")
    enc = encode(pool, lits, op, k, method=method)
    mismatches = []
    checked = 0
    for mask in range(1 << n):
        values = {v: bool((mask >> i) & 1) for i, v in enumerate(lits)}
        direct = check_direct(op, k, [values[v] for v in lits])
        sat = exists_aux_extension(enc, values, enum_limit)
        checked += 1
        if direct != sat:
            mismatches.append((values, direct, sat))
            continue
        if direct:
            witness = dict(values)
            witness.update(enc.lift(values))
            if enc.cnf.evaluate(witness) is not True:
                mismatches.append((values, "invalid lift witness", None))
    return {
        "op": op, "k": k, "n": n, "method": method,
        "status": enc.status,
        "num_clauses": enc.num_clauses,
        "num_aux": enc.num_aux,
        "checked": checked,
        "mismatches": mismatches,
        "ok": not mismatches,
    }


def count_models_per_projection(enc, total_var_limit=20):
    """Exhaustively enumerate ALL assignments (original + auxiliary).

    Returns a dict mapping each original assignment (as a tuple of booleans)
    to the number of satisfying auxiliary extensions.  Only feasible for
    small variable counts.
    """
    vars_orig = [abs(lit) for lit in enc.lits]
    total = len(vars_orig) + len(enc.aux)
    if total > total_var_limit:
        raise ValueError(f"{total} variables exceed limit {total_var_limit}")
    counts = {bits: 0 for bits in product((False, True), repeat=len(vars_orig))}
    all_vars = vars_orig + list(enc.aux)
    for bits in product((False, True), repeat=total):
        assign = dict(zip(all_vars, bits))
        if enc.cnf.evaluate(assign) is True:
            counts[bits[:len(vars_orig)]] += 1
    return counts


def verify_model_independent(enc, model):
    """Independent checker: project a claimed model onto the original
    variables and evaluate the cardinality constraint directly."""
    values = []
    for lit in enc.lits:
        v = model[abs(lit)]
        values.append(v if lit > 0 else not v)
    return check_direct(enc.op, enc.k, values)


def analyze_propagation(pool, n, op, k, method="seq", max_combos=250):
    """Measure unit-propagation (GAC) strength of an encoding.

    at-most direction : any k variables set True must force the rest False;
                        any k+1 set True must yield a conflict.
    at-least direction: any n-k variables set False must force the rest True;
                        any n-k+1 set False must yield a conflict.
    """
    lits = pool.reserve(n, "x")
    enc = encode(pool, lits, op, k, method=method)
    report = {"method": method, "op": op, "k": k, "n": n,
              "status": enc.status, "clauses": enc.num_clauses,
              "aux": enc.num_aux, "gac": None, "conflict_detected": None,
              "avg_propagated": 0.0, "trials": 0}
    if enc.status not in ("encoded", "simplified"):
        return report
    clauses = enc.cnf.clauses
    gac = True
    conflict_ok = True
    propagated = 0
    trials = 0

    def check_direction(forced, size):
        nonlocal gac, conflict_ok, propagated, trials
        if size > n:
            return
        for combo in islice(combinations(lits, size), max_combos):
            base = {v: forced for v in combo}
            after, ok = unit_propagate(clauses, base)
            trials += 1
            if not ok:
                gac = False  # consistent partial assignment falsified
                continue
            propagated += len(after) - len(base)
            for v in lits:
                if v not in base and (v not in after or after[v] == forced):
                    gac = False
        if size + 1 <= n:
            for combo in islice(combinations(lits, size + 1), max_combos):
                base = {v: forced for v in combo}
                _, ok = unit_propagate(clauses, base)
                trials += 1
                if ok:
                    conflict_ok = False  # violating assignment not detected

    if op in (ATMOST, EXACTLY):
        check_direction(True, k)
    if op in (ATLEAST, EXACTLY):
        check_direction(False, n - k)
    report["gac"] = gac
    report["conflict_detected"] = conflict_ok
    report["trials"] = trials
    report["avg_propagated"] = (propagated / trials) if trials else 0.0
    return report
