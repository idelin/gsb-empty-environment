"""Certificates and an independent verifier.

A certificate is a JSON-serialisable snapshot of the DAG: noise-symbol
radii, per-node operation specs, and the claimed affine form + interval
for every node.  ``verify_certificate`` re-derives everything from the
serialised spec alone (it never touches the original DAG object):

  1. recomputes every affine form from the node specs and checks the
     claimed center/terms exactly;
  2. checks each claimed interval equals the interval of its form;
  3. checks the noise-symbol registry matches the certificate;
  4. checks the symbol budget on every node;
  5. refuses incomplete certificates (budget truncation, failed nodes);
  6. as an independent cross-check, evaluates the real-valued function
     on a deterministic grid over the variable domains and requires all
     samples to lie inside the claimed output intervals.
"""

from __future__ import annotations

import itertools
import json
from dataclasses import dataclass, field

from . import core
from .core import AffineForm, IntervalContainsZero, NoiseSymbols
from .piecewise import PiecewiseDomainError, PiecewiseLinear

FORMAT = "affine-dag-cert/1"
SAMPLE_TOL = 1e-9
MAX_SAMPLE_POINTS = 20000


def build_certificate(dag, outputs, results) -> dict:
    outputs = [int(o) for o in outputs]
    errors = [e for r in results for e in r.errors]
    complete = all(r.complete for r in results)
    last = max(outputs) if outputs else len(dag.nodes) - 1
    nodes = []
    for node in dag.nodes[: last + 1]:
        if node.form is None:
            complete = False
            form_json = None
            interval_json = None
        else:
            form_json = {
                "center": node.form.center,
                "terms": {str(s): c for s, c in sorted(node.form.terms.items())},
            }
            lo, hi = node.form.interval(dag.symbols)
            interval_json = [lo, hi]
        nodes.append(
            {
                "id": node.nid,
                "op": node.op,
                "inputs": list(node.inputs),
                "params": _json_safe(node.params),
                "name": node.name,
                "form": form_json,
                "interval": interval_json,
            }
        )
    return {
        "format": FORMAT,
        "max_terms": dag.max_terms,
        "complete": complete,
        "errors": errors,
        "symbols": {str(s): r for s, r in sorted(dag.symbols.snapshot().items())},
        "nodes": nodes,
        "outputs": outputs,
    }


def certificate_to_json(cert: dict) -> str:
    return json.dumps(cert, sort_keys=True)


def certificate_from_json(text: str) -> dict:
    return json.loads(text)


def _json_safe(params: dict) -> dict:
    out = {}
    for key, value in params.items():
        if isinstance(value, (list, tuple)):
            out[key] = [float(v) for v in value]
        elif isinstance(value, float):
            out[key] = value
        else:
            out[key] = value
    return out


@dataclass
class VerifyResult:
    ok: bool
    complete: bool
    reasons: list[str] = field(default_factory=list)


def _claimed_form(node_json) -> AffineForm | None:
    f = node_json.get("form")
    if f is None:
        return None
    return AffineForm(f["center"], {int(s): c for s, c in f["terms"].items()})


def _recompute_node(node_json, input_forms, registry) -> AffineForm | None:
    """Verifier-side recomputation from the serialised spec only."""
    op = node_json["op"]
    p = node_json["params"]
    if op == "const":
        return core.const(p["value"])
    if op == "var":
        sid = registry.new((p["hi"] - p["lo"]) / 2.0)
        if sid != p["sid"]:
            raise _SpecMismatch(f"var symbol id {sid} != claimed {p['sid']}")
        return AffineForm((p["lo"] + p["hi"]) / 2.0, {sid: 1.0})
    if any(f is None for f in input_forms):
        return None
    if op == "add":
        return core.add(input_forms[0], input_forms[1])
    if op == "sub":
        return core.sub(input_forms[0], input_forms[1])
    if op == "scale":
        return core.scale(input_forms[0], p["factor"])
    if op == "mul":
        rem_sid = registry.new(0.0)
        if rem_sid != p["rem_sid"]:
            raise _SpecMismatch(f"mul remainder id {rem_sid} != claimed {p['rem_sid']}")
        form, rem = core.mul(input_forms[0], input_forms[1], registry)
        registry.set_radius(rem_sid, rem)
        if rem > 0.0:
            form.terms[rem_sid] = 1.0
        return form
    if op == "recip":
        rem_sid = registry.new(0.0)
        if rem_sid != p["rem_sid"]:
            raise _SpecMismatch(f"recip remainder id {rem_sid} != claimed {p['rem_sid']}")
        form, rem = core.recip(input_forms[0], registry)
        registry.set_radius(rem_sid, rem)
        if rem > 0.0:
            form.terms[rem_sid] = 1.0
        return form
    if op == "piecewise":
        rem_sid = registry.new(0.0)
        if rem_sid != p["rem_sid"]:
            raise _SpecMismatch(
                f"piecewise remainder id {rem_sid} != claimed {p['rem_sid']}"
            )
        pl = PiecewiseLinear(p["breakpoints"], p["values"])
        lo, hi = input_forms[0].interval(registry)
        lin = pl.linear_on(lo, hi)
        if lin is not None:
            slope, intercept = lin
            form = core.scale(input_forms[0], slope)
            form.center += intercept
            registry.set_radius(rem_sid, 0.0)
            return form
        rlo, rhi = pl.range_on(lo, hi)
        rem = (rhi - rlo) / 2.0
        registry.set_radius(rem_sid, rem)
        form = AffineForm((rlo + rhi) / 2.0)
        if rem > 0.0:
            form.terms[rem_sid] = 1.0
        return form
    raise _SpecMismatch(f"unknown op {op!r}")


class _SpecMismatch(ValueError):
    pass


def _eval_real(nodes, var_values, nid, memo) -> float:
    if nid in memo:
        return memo[nid]
    nd = nodes[nid]
    op = nd["op"]
    p = nd["params"]
    ins = nd["inputs"]
    if op == "const":
        val = p["value"]
    elif op == "var":
        val = var_values[nid]
    elif op == "add":
        val = _eval_real(nodes, var_values, ins[0], memo) + _eval_real(
            nodes, var_values, ins[1], memo
        )
    elif op == "sub":
        val = _eval_real(nodes, var_values, ins[0], memo) - _eval_real(
            nodes, var_values, ins[1], memo
        )
    elif op == "scale":
        val = p["factor"] * _eval_real(nodes, var_values, ins[0], memo)
    elif op == "mul":
        val = _eval_real(nodes, var_values, ins[0], memo) * _eval_real(
            nodes, var_values, ins[1], memo
        )
    elif op == "recip":
        val = 1.0 / _eval_real(nodes, var_values, ins[0], memo)
    elif op == "piecewise":
        pl = PiecewiseLinear(p["breakpoints"], p["values"])
        val = pl.eval(_eval_real(nodes, var_values, ins[0], memo))
    else:
        raise _SpecMismatch(f"unknown op {op!r}")
    memo[nid] = val
    return val


def verify_certificate(cert: dict) -> VerifyResult:
    reasons: list[str] = []
    if cert.get("format") != FORMAT:
        return VerifyResult(False, False, [f"unsupported format {cert.get('format')!r}"])
    nodes = cert.get("nodes", [])
    symbols_claimed = {int(s): r for s, r in cert.get("symbols", {}).items()}
    max_terms = cert.get("max_terms")

    registry = NoiseSymbols()
    recomputed: list[AffineForm | None] = []
    for nd in nodes:
        inputs = nd.get("inputs", [])
        if any(not (0 <= i < len(recomputed)) for i in inputs):
            reasons.append(f"node {nd.get('id')}: input index out of range")
            recomputed.append(None)
            continue
        try:
            form = _recompute_node(nd, [recomputed[i] for i in inputs], registry)
        except (IntervalContainsZero, PiecewiseDomainError) as exc:
            reasons.append(f"node {nd.get('id')} ({nd.get('op')}): {exc}")
            recomputed.append(None)
            continue
        except _SpecMismatch as exc:
            reasons.append(f"node {nd.get('id')}: {exc}")
            recomputed.append(None)
            continue
        recomputed.append(form)

        claimed = _claimed_form(nd)
        if form is None or claimed is None:
            if form is not claimed:
                reasons.append(f"node {nd.get('id')}: form presence mismatch")
            continue
        if form != claimed:
            reasons.append(
                f"node {nd.get('id')}: claimed affine form does not match "
                f"recomputed form"
            )
        lo, hi = form.interval(registry)
        if nd.get("interval") != [lo, hi]:
            reasons.append(f"node {nd.get('id')}: claimed interval mismatch")
        if max_terms is not None and form.num_terms() > max_terms:
            reasons.append(
                f"node {nd.get('id')}: noise-symbol budget exceeded "
                f"({form.num_terms()} terms > budget {max_terms})"
            )

    if registry.snapshot() != symbols_claimed:
        reasons.append("noise-symbol registry does not match certificate")

    complete = bool(cert.get("complete", False))
    if not complete:
        errs = "; ".join(cert.get("errors", [])) or "no error details recorded"
        reasons.append(f"certificate is incomplete: {errs}")

    reasons.extend(_sample_check(cert, recomputed))
    return VerifyResult(ok=not reasons, complete=complete, reasons=reasons)


def _sample_check(cert: dict, recomputed) -> list[str]:
    """Independent cross-check: real-valued grid samples must lie inside the
    claimed output intervals."""
    nodes = cert.get("nodes", [])
    outputs = cert.get("outputs", [])
    var_ids = [nd["id"] for nd in nodes if nd["op"] == "var"]
    if not var_ids or not outputs:
        return []
    grids = []
    for vid in var_ids:
        p = nodes[vid]["params"]
        lo, hi = p["lo"], p["hi"]
        grids.append([lo + (hi - lo) * k / 4.0 for k in range(5)])
    total = 1
    for g in grids:
        total *= len(g)
    if total > MAX_SAMPLE_POINTS:
        return []
    reasons = []
    for combo in itertools.product(*grids):
        var_values = dict(zip(var_ids, combo))
        memo: dict[int, float] = {}
        for out in outputs:
            if recomputed[out] is None:
                continue
            claimed_interval = nodes[out].get("interval")
            if claimed_interval is None:
                continue
            try:
                val = _eval_real(nodes, var_values, out, memo)
            except (ArithmeticError, PiecewiseDomainError):
                continue
            lo, hi = claimed_interval
            tol = SAMPLE_TOL * max(1.0, abs(lo), abs(hi))
            if not (lo - tol <= val <= hi + tol):
                reasons.append(
                    f"sample check failed at output node {out}: value {val!r} "
                    f"outside claimed interval [{lo!r}, {hi!r}]"
                )
                return reasons
    return reasons
