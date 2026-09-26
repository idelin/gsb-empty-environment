"""Independent certificate verifier.

The verifier trusts *nothing* produced by the generator except the raw
claims: ``generator_stats`` and any other advisory fields are ignored.  Every
fact is re-derived from the declared finite model (its transition table,
initial states and bad-state predicate):

* reachability closure: initial coverage, successor closure, safety;
* counterexample: first state initial, every transition exists in the model,
  final state bad, transitions replayed from the path.

Unknown/partial explorations cannot masquerade as certificates and are
rejected.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from enum import Enum
from typing import Optional

from .model import FiniteModel


class VerificationVerdict(str, Enum):
    VALID = "valid"
    INVALID = "invalid"
    UNKNOWN = "unknown"


@dataclass(frozen=True)
class Finding:
    code: str
    message: str
    detail: dict = field(default_factory=dict)

    def __str__(self) -> str:  # pragma: no cover - trivial
        return f"{self.code}: {self.message}"


def _finding(code: str, message: str, **detail: object) -> Finding:
    return Finding(code=code, message=message, detail=dict(detail))


def _check_fingerprint(model: FiniteModel, certificate: dict) -> list[Finding]:
    claimed = certificate.get("model", {}).get("fingerprint")
    if claimed != model.fingerprint:
        return [
            _finding(
                "FINGERPRINT_MISMATCH",
                "certificate was not produced for this model",
                claimed=claimed,
                expected=model.fingerprint,
            )
        ]
    return []


def verify_reachability(
    model: FiniteModel, certificate: dict
) -> tuple[VerificationVerdict, list[Finding]]:
    """Verify a reachability-closure certificate against ``model``."""
    findings = _check_fingerprint(model, certificate)
    kind = certificate.get("kind")
    if kind != "reachability_closure":
        findings.append(
            _finding("WRONG_KIND", "expected a reachability_closure certificate", kind=kind)
        )
        return VerificationVerdict.INVALID, findings
    if findings:
        return VerificationVerdict.INVALID, findings

    raw_states = certificate.get("reachable_states")
    raw_initial = certificate.get("initial")
    raw_successors = certificate.get("successors")
    if not isinstance(raw_states, list):
        findings.append(_finding("MALFORMED", "reachable_states must be a list"))
        return VerificationVerdict.INVALID, findings
    if not isinstance(raw_initial, list):
        findings.append(_finding("MALFORMED", "initial must be a list"))
        return VerificationVerdict.INVALID, findings
    if not isinstance(raw_successors, dict):
        findings.append(_finding("MALFORMED", "successors must be an object"))
        return VerificationVerdict.INVALID, findings

    try:
        reachable = set(raw_states)
    except TypeError:
        findings.append(_finding("MALFORMED", "reachable states are not hashable"))
        return VerificationVerdict.INVALID, findings
    if len(reachable) != len(raw_states):
        findings.append(
            _finding("DUPLICATE_STATE", "reachable_states contains duplicates")
        )

    unknown = [s for s in reachable if s not in model.states]
    if unknown:
        findings.append(
            _finding(
                "UNKNOWN_STATE",
                "certificate lists states absent from the model",
                states=unknown,
            )
        )

    initial_claims = set(raw_initial)
    missing_initial = sorted(
        model.initial - initial_claims,
        key=lambda s: json.dumps(s, sort_keys=True),
    )
    extra_initial = sorted(
        initial_claims - model.initial,
        key=lambda s: json.dumps(s, sort_keys=True),
    )
    for state in missing_initial:
        findings.append(
            _finding(
                "INITIAL_NOT_COVERED",
                "an initial state is missing from the closure",
                state=state,
            )
        )
    for state in extra_initial:
        findings.append(
            _finding(
                "FALSE_INITIAL",
                "certificate claims a non-initial state is initial",
                state=state,
            )
        )

    claimed_successors: dict[object, object] = {}
    for key, targets in raw_successors.items():
        try:
            source = json.loads(key)
        except (json.JSONDecodeError, TypeError):
            findings.append(
                _finding("MALFORMED", f"successor key is not JSON: {key!r}")
            )
            continue
        if not isinstance(targets, list):
            findings.append(
                _finding(
                    "MALFORMED",
                    "successor list must be a list",
                    state=source,
                )
            )
            continue
        claimed_successors[source] = targets

    for state in sorted(reachable, key=lambda s: json.dumps(s, sort_keys=True)):
        if state not in model.states:
            continue
        if state not in claimed_successors:
            findings.append(
                _finding(
                    "SUCCESSOR_TABLE_MISSING",
                    "a reachable state has no successor entry",
                    state=state,
                )
            )
            continue
        actual = set(model.successors(state))
        claimed = set(claimed_successors[state])
        for target in sorted(actual - claimed, key=lambda s: json.dumps(s, sort_keys=True)):
            findings.append(
                _finding(
                    "SUCCESSOR_NOT_CLOSED",
                    "a required successor state is missing from the certificate",
                    state=state,
                    missing_successor=target,
                )
            )
        for target in sorted(claimed - actual, key=lambda s: json.dumps(s, sort_keys=True)):
            findings.append(
                _finding(
                    "PHANTOM_SUCCESSOR",
                    "certificate lists a successor the model does not have",
                    state=state,
                    phantom_successor=target,
                )
            )

    for state in sorted(reachable, key=lambda s: json.dumps(s, sort_keys=True)):
        if model.is_bad(state):
            findings.append(
                _finding(
                    "PROPERTY_VIOLATED",
                    "a state in the claimed-safe closure violates the invariant",
                    state=state,
                )
            )

    for source in claimed_successors:
        if source not in reachable:
            findings.append(
                _finding(
                    "EXTRA_SUCCESSOR_ENTRY",
                    "successor table includes a state outside the closure",
                    state=source,
                )
            )

    # Independent re-derivation: nothing outside the certificate can be
    # reached from an initial state using only declared transitions.
    independent: set[object] = set()
    stack = list(model.initial)
    while stack:
        state = stack.pop()
        if state in independent:
            continue
        independent.add(state)
        for nxt in model.successors(state):
            if nxt not in independent:
                stack.append(nxt)
    uncovered = sorted(
        independent - reachable, key=lambda s: json.dumps(s, sort_keys=True)
    )
    if uncovered:
        findings.append(
            _finding(
                "REACHABILITY_INCOMPLETE",
                "independently reachable states are absent from the closure",
                states=uncovered,
            )
        )

    verdict = VerificationVerdict.INVALID if findings else VerificationVerdict.VALID
    return verdict, findings


def verify_counterexample(
    model: FiniteModel, certificate: dict
) -> tuple[VerificationVerdict, list[Finding]]:
    """Replay a counterexample step by step against ``model``."""
    findings = _check_fingerprint(model, certificate)
    kind = certificate.get("kind")
    if kind != "counterexample":
        findings.append(
            _finding("WRONG_KIND", "expected a counterexample certificate", kind=kind)
        )
        return VerificationVerdict.INVALID, findings
    if findings:
        return VerificationVerdict.INVALID, findings

    path = certificate.get("path")
    transitions = certificate.get("transitions")
    if not isinstance(path, list) or not path:
        findings.append(_finding("MALFORMED", "path must be a non-empty list"))
        return VerificationVerdict.INVALID, findings
    if not isinstance(transitions, list):
        findings.append(_finding("MALFORMED", "transitions must be a list"))
        return VerificationVerdict.INVALID, findings

    for index, state in enumerate(path):
        if state not in model.states:
            findings.append(
                _finding(
                    "UNKNOWN_STATE",
                    "path visits a state absent from the model",
                    path_index=index,
                    state=state,
                )
            )

    if path[0] not in model.initial:
        findings.append(
            _finding(
                "NOT_INITIAL",
                "counterexample does not start in an initial state",
                state=path[0],
            )
        )

    if len(transitions) != len(path) - 1:
        findings.append(
            _finding(
                "TRANSITION_LENGTH_MISMATCH",
                "number of transition records does not match the path",
                transitions=len(transitions),
                expected=len(path) - 1,
            )
        )

    # Replay the path directly against the model's transition table.
    for index in range(len(path) - 1):
        source = path[index]
        target = path[index + 1]
        if source not in model.states:
            findings.append(
                _finding(
                    "BAD_TRANSITION",
                    "transition source is not a model state",
                    transition_index=index,
                    source=source,
                    target=target,
                )
            )
            continue
        if target not in model.successors(source):
            findings.append(
                _finding(
                    "BAD_TRANSITION",
                    "no such transition in the model",
                    transition_index=index,
                    source=source,
                    target=target,
                    available=list(model.successors(source)),
                )
            )

    # Cross-check the transition records and pinpoint any inconsistent record.
    for pos, record in enumerate(transitions):
        if not isinstance(record, dict):
            findings.append(
                _finding("MALFORMED", "transition record must be an object", position=pos)
            )
            continue
        index = record.get("index", pos)
        if index != pos:
            findings.append(
                _finding(
                    "TRANSITION_INDEX",
                    "transition record has a wrong index",
                    position=pos,
                    claimed_index=index,
                )
            )
        if pos >= len(path) - 1:
            continue
        if record.get("source") != path[pos] or record.get("target") != path[pos + 1]:
            findings.append(
                _finding(
                    "TRANSITION_RECORD_MISMATCH",
                    "transition record does not match the path",
                    transition_index=pos,
                    record_source=record.get("source"),
                    record_target=record.get("target"),
                    path_source=path[pos],
                    path_target=path[pos + 1],
                )
            )

    bad_state = certificate.get("bad_state", path[-1])
    if bad_state != path[-1]:
        findings.append(
            _finding(
                "BAD_STATE_MISMATCH",
                "bad_state field does not match the end of the path",
                field_value=bad_state,
                path_end=path[-1],
            )
        )
    if path[-1] not in model.bad_states:
        findings.append(
            _finding(
                "END_NOT_BAD",
                "counterexample ends in a state that satisfies the invariant",
                state=path[-1],
            )
        )

    verdict = VerificationVerdict.INVALID if findings else VerificationVerdict.VALID
    return verdict, findings


def verify_certificate(
    model: FiniteModel, certificate: dict
) -> tuple[VerificationVerdict, list[Finding]]:
    """Dispatch verification based on the certificate kind.

    Unknown/partial-exploration payloads can never be accepted as complete
    safety evidence: they are invalid certificates.
    """
    kind = certificate.get("kind") if isinstance(certificate, dict) else None
    if kind == "reachability_closure":
        return verify_reachability(model, certificate)
    if kind == "counterexample":
        return verify_counterexample(model, certificate)
    return (
        VerificationVerdict.INVALID,
        [
            _finding(
                "UNSUPPORTED_KIND",
                "not a verifiable complete certificate (partial exploration?)",
                kind=kind,
            )
        ],
    )
