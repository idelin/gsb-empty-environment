"""Evidence export and deterministic replay.

Evidence is a plain JSON-serializable dict. Replaying it rebuilds the graph,
re-canonicalizes it and re-derives every conclusion through the registered
rule semantics, so the recorded digest must be reproducible from the
evidence alone. Cyclic or tampered evidence is rejected.
"""

from __future__ import annotations

from typing import Callable, Dict, Mapping, Optional, Tuple

from .canonical import CanonicalForm, DigestFn, canonicalize, default_digest
from .errors import ReplayError
from .model import Fact, Rule

EVIDENCE_FORMAT = "proofdag-evidence/1"
# Maps (rule_id, rule_version) -> f(premise_payloads) -> conclusion_payload
Semantics = Mapping[Tuple[str, int], Callable[[Tuple[str, ...]], str]]


def replay(
    evidence: dict,
    semantics: Optional[Semantics] = None,
    digest_fn: DigestFn = default_digest,
) -> CanonicalForm:
    if evidence.get("format") != EVIDENCE_FORMAT:
        raise ReplayError(
            f"unsupported evidence format: {evidence.get('format')!r}"
        )
    graph = {}
    for entry in evidence["nodes"]:
        fact = Fact(**entry["fact"])
        if entry["kind"] == "asserted":
            graph[entry["number"]] = (fact, None, ())
        elif entry["kind"] == "derived":
            graph[entry["number"]] = (
                fact,
                Rule(**entry["rule"]),
                tuple(entry["premises"]),
            )
        else:
            raise ReplayError(f"unknown node kind: {entry['kind']!r}")
    form = canonicalize(graph, tuple(evidence["roots"]), digest_fn)
    if form.digest != evidence["digest"]:
        raise ReplayError(
            "evidence digest mismatch: structure does not reproduce the "
            "recorded digest"
        )
    if semantics is not None:
        _verify_semantics(form, semantics)
    return form


def _verify_semantics(form: CanonicalForm, semantics: Semantics) -> None:
    payloads: Dict[int, str] = {}

    def resolve(number: int) -> str:
        if number in payloads:
            return payloads[number]
        node = form.nodes[number]
        if node.rule is None:
            payloads[number] = node.fact.payload
            return payloads[number]
        premise_payloads = tuple(resolve(p) for p in node.premises)
        derive = semantics.get((node.rule.rule_id, node.rule.version))
        if derive is None:
            raise ReplayError(
                f"no semantics registered for rule "
                f"{node.rule.rule_id!r} v{node.rule.version}"
            )
        expected = derive(premise_payloads)
        if expected != node.fact.payload:
            raise ReplayError(
                f"rule {node.rule.rule_id!r} does not derive conclusion "
                f"{node.fact.payload!r} from {premise_payloads!r} "
                f"(expected {expected!r})"
            )
        payloads[number] = node.fact.payload
        return payloads[number]

    for node in form.nodes:
        resolve(node.number)
