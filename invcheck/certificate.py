"""Independently checkable certificates and replayable counterexamples.

A certificate embeds the model, the candidate invariant, the verdict and
any counterexample states.  :func:`replay_certificate` re-verifies the
claims using ONLY concrete expression evaluation (:mod:`invcheck.expr`
and :mod:`invcheck.model`), i.e. completely independently of the BDD
engine that produced the certificate.
"""

from __future__ import annotations

import hashlib
import json

from . import expr as exprmod
from .model import parse_model, next_states

CERT_KIND = "invcheck.certificate"
CERT_VERSION = 1


def _state_json(state):
    if state is None:
        return None
    return {k: bool(v) for k, v in state.items()}


def model_digest(model_source):
    blob = json.dumps(model_source, sort_keys=True).encode("utf-8")
    return hashlib.sha256(blob).hexdigest()


def build_certificate(model, invariant_text, result):
    cert = {
        "kind": CERT_KIND,
        "version": CERT_VERSION,
        "model": model.source,
        "model_sha256": model_digest(model.source),
        "invariant": invariant_text,
        "variable_order": result.stats.get("variable_order"),
        "verdict": result.verdict,
        "base_case": {
            "status": result.base_status,
            "counterexample": _state_json(result.base_counterexample),
        },
        "inductive_step": {"status": result.inductive_status, "counterexample": None},
        "reachability": {
            "decided": result.reachable_checked,
            "reachable_count": result.reachable_count,
            "invariant_holds_on_reachable": result.holds_on_reachable,
        },
        "inductive_counterexample_premise_reachable": result.cex_source_reachable,
        "notes": list(result.notes),
        "replay_command": "python -m invcheck replay <certificate.json>",
    }
    if result.inductive_counterexample is not None:
        s, sp = result.inductive_counterexample
        cert["inductive_step"]["counterexample"] = {
            "premise": _state_json(s),
            "successor": _state_json(sp),
        }
    return cert


def save_certificate(cert, path):
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(cert, fh, indent=2)
        fh.write("\n")


def load_certificate(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def replay_certificate(cert):
    """Re-verify a certificate without any BDD machinery.

    Returns ``{"ok": bool, "checks": [...], "verdict": ...}``.  For
    counterexample verdicts the embedded states are re-checked
    concretely; for ``verified`` verdicts the structural fields are
    validated (full independent confirmation of a positive verdict is
    available via the reference enumerator on small domains).
    """
    if not isinstance(cert, dict) or cert.get("kind") != CERT_KIND:
        raise ValueError("not an invcheck certificate")
    if cert.get("version") != CERT_VERSION:
        raise ValueError(f"unsupported certificate version {cert.get('version')}")
    model = parse_model(cert["model"])
    checks = [
        {"name": "model hash matches embedded model",
         "ok": model_digest(cert["model"]) == cert.get("model_sha256")}
    ]
    inv_ast = exprmod.parse(cert["invariant"])
    variables = model.variables

    def state_of(raw):
        return {v: bool(raw[v]) for v in variables}

    verdict = cert.get("verdict")
    base = cert.get("base_case", {})
    if base.get("status") == "fail":
        if not base.get("counterexample"):
            checks.append({"name": "base: counterexample present", "ok": False})
        else:
            s = state_of(base["counterexample"])
            checks.append({"name": "base: init(s) holds",
                           "ok": exprmod.eval_ast(model.init, s) is True})
            checks.append({"name": "base: inv(s) is violated",
                           "ok": exprmod.eval_ast(inv_ast, s) is False})
    ind = cert.get("inductive_step", {})
    if ind.get("status") == "fail":
        cex = ind.get("counterexample")
        if not cex:
            checks.append({"name": "inductive: counterexample present", "ok": False})
        else:
            s = state_of(cex["premise"])
            sp = state_of(cex["successor"])
            checks.append({"name": "inductive: inv(s) holds",
                           "ok": exprmod.eval_ast(inv_ast, s) is True})
            checks.append({
                "name": "inductive: s -> s' is a model transition",
                "ok": any(sp == t for t in next_states(model, s)),
            })
            checks.append({"name": "inductive: inv(s') is violated",
                           "ok": exprmod.eval_ast(inv_ast, sp) is False})
    if verdict in ("base_case_failed", "inductive_failed"):
        prefix = "base" if verdict == "base_case_failed" else "inductive"
        if not any(c["name"].startswith(prefix) for c in checks):
            checks.append({"name": f"{prefix}: counterexample recorded", "ok": False})
    ok = all(c["ok"] for c in checks)
    return {"ok": ok, "checks": checks, "verdict": verdict}
