"""Independently checkable certificates and replayable counterexamples.

A certificate is a self-contained JSON document: the model, the candidate
invariant, the verdict, and (on failure) a minimal counterexample cube.
Verification re-derives everything from scratch with a *fresh* BDD manager
using a *different* variable order, and replays counterexamples against the
AST-level semantics (no BDDs), so a buggy prover cannot certify itself.
"""

from __future__ import annotations

import hashlib
import json

from . import expr as expr_mod
from .bdd import BDDManager
from .checker import (FAIL_BASE, FAIL_STEP, PASS, UNKNOWN, InvariantChecker)
from .model import load_model, next_name

FORMAT_VERSION = 1


def _model_digest(model_dict):
    blob = json.dumps(model_dict, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def build_certificate(model, result):
    """Create a self-contained certificate dict from a CheckResult."""
    model = load_model(model)
    model_dict = model.canonical_dict()
    return {
        "format_version": FORMAT_VERSION,
        "model": model_dict,
        "model_sha256": _model_digest(model_dict),
        "invariant": result.invariant,
        "variable_order": list(result.variable_order),
        "verdict": result.verdict,
        "init_empty": result.init_empty,
        "base_ok": result.base_ok,
        "base_cex": result.base_cex,
        "step_ok": result.step_ok,
        "step_cex": result.step_cex.to_dict() if result.step_cex else None,
        "reachable_complete": result.reachable_complete,
        "reachable_states": result.reachable_states,
        "holds_on_reachable": result.holds_on_reachable,
        "unknown_reason": result.unknown_reason,
        "notes": list(result.notes),
        "stats": dict(result.stats),
    }


def save_certificate(cert, path):
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(cert, fh, indent=2, sort_keys=True)
        fh.write("\n")


def load_certificate(source):
    if isinstance(source, dict):
        return source
    try:
        with open(source, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, TypeError):
        return json.loads(source)


def _alt_order(variables):
    """A deliberately different order: reversed interleaving."""
    return list(reversed(variables))


def _concrete(cube, variables):
    """Deterministically extend a partial cube to a full assignment."""
    return {v: bool(cube.get(v, False)) for v in variables}


def replay_certificate(cert):
    """Replay the counterexample against AST-level semantics.

    Returns a list of human-readable lines; raises ValueError if a claimed
    counterexample does not actually replay.
    """
    cert = load_certificate(cert)
    model = load_model(cert["model"])
    inv_ast = expr_mod.parse(cert["invariant"])
    lines = []
    base_cex = cert.get("base_cex")
    if base_cex:
        state = _concrete(base_cex, model.variables)
        init_ok = model.init_holds(state)
        inv_ok = model.invariant_holds(state, inv_ast)
        lines.append("replay base cex: state=%s init=%s invariant=%s"
                     % (_fmt_state(model, state), init_ok, inv_ok))
        if not (init_ok and not inv_ok):
            raise ValueError("base counterexample does not replay")
    step_cex = cert.get("step_cex")
    if step_cex:
        state = _concrete(step_cex["state"], model.variables)
        nxt = _concrete(step_cex["next_state"], model.variables)
        inv_s = model.invariant_holds(state, inv_ast)
        trans = model.transition_holds(state, nxt)
        inv_t = model.invariant_holds(nxt, inv_ast)
        lines.append("replay step cex: %s -> %s invariant(s)=%s transition=%s "
                     "invariant(s')=%s"
                     % (_fmt_state(model, state), _fmt_state(model, nxt),
                        inv_s, trans, inv_t))
        if not (inv_s and trans and not inv_t):
            raise ValueError("step counterexample does not replay")
    if not lines:
        lines.append("nothing to replay (verdict %s)" % cert["verdict"])
    return lines


def _fmt_state(model, state):
    return "".join("1" if state.get(v, False) else "0" for v in model.variables)


def verify_certificate(cert, use_alt_order=True):
    """Independently verify a certificate.  Returns a report dict."""
    cert = load_certificate(cert)
    checks = []

    def record(name, ok, detail=""):
        checks.append({"check": name, "ok": bool(ok), "detail": detail})
        return ok

    model_dict = cert["model"]
    record("model_digest", _model_digest(model_dict) == cert["model_sha256"],
           "sha256 of canonical model encoding")
    model = load_model(model_dict)

    order = (_alt_order(model.all_variables()) if use_alt_order
             else list(cert["variable_order"]))
    record("fresh_variable_order", order != list(cert["variable_order"])
           or not use_alt_order,
           "verifier uses its own variable order: %s" % (order,))

    checker = InvariantChecker(model, variable_order=order)
    result = checker.check(cert["invariant"])
    record("verdict", result.verdict == cert["verdict"],
           "recomputed=%s certified=%s" % (result.verdict, cert["verdict"]))
    record("init_empty", result.init_empty == cert["init_empty"])
    record("base_ok", result.base_ok == cert["base_ok"])
    record("step_ok", result.step_ok == cert["step_ok"])
    if result.reachable_complete and cert.get("reachable_complete"):
        record("reachable_states",
               result.reachable_states == cert["reachable_states"],
               "recomputed=%s certified=%s"
               % (result.reachable_states, cert["reachable_states"]))
        record("holds_on_reachable",
               result.holds_on_reachable == cert["holds_on_reachable"])

    # Re-derive the bad sets with the fresh manager and check that each
    # certified cube is (a) contained in the bad set, (b) irredundant.
    mgr = checker.mgr
    inv = expr_mod.to_bdd(mgr, expr_mod.parse(cert["invariant"]))
    if cert["verdict"] in (FAIL_BASE,) or cert.get("base_cex"):
        bad = mgr.and_(checker.init_bdd, mgr.negate(inv))
        _verify_cube(mgr, bad, cert.get("base_cex"), "base_cex", record)
    if cert["verdict"] in (FAIL_STEP,) or cert.get("step_cex"):
        inv_next = mgr.rename(inv, checker._cur_to_next)
        bad = mgr.and_(mgr.and_(inv, checker.trans_bdd), mgr.negate(inv_next))
        cube = {}
        if cert.get("step_cex"):
            for k, v in cert["step_cex"]["state"].items():
                cube[k] = v
            for k, v in cert["step_cex"]["next_state"].items():
                cube[next_name(k)] = v
        _verify_cube(mgr, bad, cube if cert.get("step_cex") else None,
                     "step_cex", record)

    # Replay counterexamples through the BDD-free semantics.
    try:
        lines = replay_certificate(cert)
        record("replay", True, "; ".join(lines))
    except ValueError as exc:
        record("replay", False, str(exc))

    return {"ok": all(c["ok"] for c in checks), "checks": checks}


def _verify_cube(mgr, bad, cube, label, record):
    if cube is None:
        record(label + "_present", False, "certificate claims failure but "
               "carries no counterexample")
        return
    cube_bdd = mgr.cube(cube)
    nonempty = cube_bdd != mgr.FALSE
    contained = mgr.implies_true(cube_bdd, bad)
    record(label + "_valid", nonempty and contained,
           "cube is nonempty and contained in the bad set")
    minimal = True
    for name in cube:
        trial = {k: v for k, v in cube.items() if k != name}
        if mgr.implies_true(mgr.cube(trial), bad):
            minimal = False
            break
    record(label + "_minimal", minimal,
           "dropping any literal leaves the bad set (irredundant)")
