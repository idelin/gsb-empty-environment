"""Coverage / conflict analyzer for priority-ordered rule sets.

Deterministic analyses:
  * coverage relations between rule condition spaces,
  * unreachable low-priority rules fully covered by higher-priority rules
    (single-rule subsumption first, then exact union coverage by bounded
    enumeration),
  * same-priority conflicts: overlapping rules with different effects,
  * partial overlaps: only a concrete counterexample inside the overlap is
    reported (the lower-priority rule stays reachable).

Findings carry stable content-based ids and the analyzer version. Every
finding can be re-checked with :func:`replay`. If the operation budget is
exhausted the report is marked incomplete; the analyzer never claims that
conflicts or unreachable rules are absent in that case.
"""

from __future__ import annotations

import hashlib
import itertools
import json
from dataclasses import dataclass
from typing import Any, Iterable

from model import Condition, FactSchema, Rule, rule_matches, rule_ref

ANALYZER_VERSION = 1


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    kind: str
    subjects: tuple[str, ...]
    detail: str


@dataclass(frozen=True)
class Finding:
    finding_id: str
    version: int
    kind: str  # "conflict" | "unreachable" | "partial_shadow"
    rule_refs: tuple[str, ...]
    counterexample: tuple[tuple[str, Any], ...] | None
    detail: str


@dataclass
class Report:
    schema_ref: str
    analyzer_version: int
    complete: bool
    budget_used: int
    budget_max: int
    steps: list[Step]
    findings: list[Finding]
    coverage: list[tuple[str, str]]

    def of_kind(self, kind: str) -> list[Finding]:
        return [finding for finding in self.findings if finding.kind == kind]

    def summary(self) -> str:
        status = (
            "COMPLETE"
            if self.complete
            else "INCOMPLETE: budget exhausted, absence of conflicts / "
            "unreachable rules is NOT established"
        )
        return "\n".join(
            [
                f"schema={self.schema_ref} analyzer_version={self.analyzer_version}",
                f"status={status}",
                f"budget={self.budget_used}/{self.budget_max} steps={len(self.steps)}",
                f"coverage_pairs={len(self.coverage)} findings={len(self.findings)}",
            ]
        )


class _Budget:
    def __init__(self, max_operations: int):
        self.max_operations = max_operations
        self.used = 0

    def consume(self, n: int = 1) -> bool:
        if self.used + n > self.max_operations:
            return False
        self.used += n
        return True


def _validate(rules: Iterable[Rule], schema: FactSchema) -> None:
    for rule in rules:
        for field_name, cond in rule.conditions.items():
            if field_name not in schema.domains:
                raise ValueError(
                    f"{rule_ref(rule)} constrains unknown field {field_name!r}"
                )
            if not schema.domains[field_name].covers(cond):
                raise ValueError(
                    f"{rule_ref(rule)} condition on {field_name!r} escapes "
                    f"the domain of fact {schema.ref}"
                )


def _intersect_space(a: Rule, b: Rule) -> dict[str, Condition] | None:
    """Combined per-field constraints of two rules; None if disjoint."""
    space: dict[str, Condition] = {}
    for field_name in set(a.conditions) | set(b.conditions):
        ca = a.conditions.get(field_name)
        cb = b.conditions.get(field_name)
        if ca is None:
            cond = cb
        elif cb is None:
            cond = ca
        else:
            cond = ca.intersect(cb)
        if cond is None:
            return None
        space[field_name] = cond
    return space


def _covers_space(a: Rule, b: Rule, schema: FactSchema) -> bool:
    """True iff every input matched by ``b`` is also matched by ``a``."""
    for field_name, cond in a.conditions.items():
        other = b.conditions.get(field_name, schema.domains[field_name])
        if not cond.covers(other):
            return False
    return True


def _pick_input(space: dict[str, Condition], schema: FactSchema) -> dict[str, Any]:
    """Deterministic concrete input inside ``space`` (all schema fields)."""
    return {
        field_name: (
            space[field_name]
            if field_name in space
            else schema.domains[field_name]
        ).pick()
        for field_name in sorted(schema.domains)
    }


def _complete_input(point: dict[str, Any], schema: FactSchema) -> dict[str, Any]:
    full = {name: cond.pick() for name, cond in schema.domains.items()}
    full.update(point)
    return full


def _space_points(
    fields: list[str], constraints: dict[str, Condition]
) -> Iterable[dict[str, Any]]:
    pools = [constraints[field_name].sorted_values() for field_name in fields]
    for combo in itertools.product(*pools):
        yield dict(zip(fields, combo))


def _make_finding(
    kind: str,
    refs: Iterable[str],
    counterexample: dict[str, Any] | None,
    detail: str,
) -> Finding:
    refs_tuple = tuple(refs)
    cx_pairs = (
        tuple(sorted(counterexample.items()))
        if counterexample is not None
        else None
    )
    payload = json.dumps(
        {"v": ANALYZER_VERSION, "kind": kind, "refs": refs_tuple, "cx": cx_pairs},
        sort_keys=True,
        default=str,
    )
    finding_id = "F-" + hashlib.sha1(payload.encode("utf-8")).hexdigest()[:12]
    return Finding(finding_id, ANALYZER_VERSION, kind, refs_tuple, cx_pairs, detail)


def analyze(
    rules: Iterable[Rule],
    schema: FactSchema,
    max_operations: int = 100_000,
) -> Report:
    ordered = sorted(rules, key=lambda r: (r.rule_id, r.version))
    _validate(ordered, schema)

    budget = _Budget(max_operations)
    steps: list[Step] = []
    findings: list[Finding] = []
    coverage: list[tuple[str, str]] = []
    complete = True
    sequence = itertools.count(1)

    def record(kind: str, subjects: Iterable[str], detail: str) -> None:
        steps.append(
            Step(
                f"step-{next(sequence):04d}",
                ANALYZER_VERSION,
                kind,
                tuple(subjects),
                detail,
            )
        )

    # Phase 1: pairwise relations (closed form, one budget unit per pair).
    stop = False
    for i, a in enumerate(ordered):
        if stop:
            break
        for b in ordered[i + 1 :]:
            if not budget.consume():
                complete = False
                stop = True
                record(
                    "budget_exhausted",
                    [],
                    "pairwise checks truncated; "
                    "absence of conflicts not established",
                )
                break
            space = _intersect_space(a, b)
            record(
                "pair_check",
                [rule_ref(a), rule_ref(b)],
                "overlap" if space is not None else "disjoint",
            )
            if space is None:
                continue
            if _covers_space(a, b, schema):
                coverage.append((rule_ref(a), rule_ref(b)))
            if _covers_space(b, a, schema):
                coverage.append((rule_ref(b), rule_ref(a)))
            if a.priority == b.priority:
                if a.effect != b.effect:
                    cx = _pick_input(space, schema)
                    findings.append(
                        _make_finding(
                            "conflict",
                            [rule_ref(a), rule_ref(b)],
                            cx,
                            f"same priority {a.priority}, effects "
                            f"{a.effect!r} vs {b.effect!r}",
                        )
                    )
            else:
                high, low = (a, b) if a.priority > b.priority else (b, a)
                if not _covers_space(high, low, schema):
                    cx = _pick_input(space, schema)
                    findings.append(
                        _make_finding(
                            "partial_shadow",
                            [rule_ref(high), rule_ref(low)],
                            cx,
                            f"{rule_ref(high)} shadows part of {rule_ref(low)}",
                        )
                    )

    # Phase 2: unreachability of low-priority rules.
    unreachable: set[str] = set()
    for low in ordered:
        higher = [r for r in ordered if r.priority > low.priority]
        if not higher:
            continue
        subsumed_by = next(
            (h for h in higher if _covers_space(h, low, schema)), None
        )
        if subsumed_by is not None:
            record(
                "unreachable",
                [rule_ref(low)],
                f"subsumed by {rule_ref(subsumed_by)} (closed form)",
            )
            findings.append(
                _make_finding(
                    "unreachable",
                    [rule_ref(low), rule_ref(subsumed_by)],
                    None,
                    f"fully covered by higher-priority {rule_ref(subsumed_by)}",
                )
            )
            unreachable.add(rule_ref(low))
            continue
        # Exact union-coverage check by bounded enumeration.
        fields = sorted(
            set(low.conditions)
            | {field for h in higher for field in h.conditions}
        )
        constraints = {
            field: low.conditions.get(field, schema.domains[field])
            for field in fields
        }
        size = 1
        for field in fields:
            size *= constraints[field].size()
        if not budget.consume(size):
            complete = False
            record(
                "budget_exhausted",
                [rule_ref(low)],
                f"union-coverage undecided: {size} points required, "
                f"{budget.max_operations - budget.used} left",
            )
            continue
        witness = None
        for point in _space_points(fields, constraints):
            full = _complete_input(point, schema)
            if not any(rule_matches(h, full) for h in higher):
                witness = full
                break
        if witness is None:
            refs = [rule_ref(low)] + [rule_ref(h) for h in higher]
            record(
                "unreachable",
                [rule_ref(low)],
                "covered by union of higher-priority rules",
            )
            findings.append(
                _make_finding(
                    "unreachable",
                    refs,
                    None,
                    "covered by union of higher-priority rules: "
                    + ", ".join(refs[1:]),
                )
            )
            unreachable.add(rule_ref(low))
        else:
            record(
                "reachable_witness",
                [rule_ref(low)],
                f"uncovered input: {witness}",
            )

    # A rule proven unreachable is not reported as partially shadowed.
    findings = [
        finding
        for finding in findings
        if not (
            finding.kind == "partial_shadow"
            and finding.rule_refs[1] in unreachable
        )
    ]

    return Report(
        schema_ref=schema.ref,
        analyzer_version=ANALYZER_VERSION,
        complete=complete,
        budget_used=budget.used,
        budget_max=budget.max_operations,
        steps=steps,
        findings=findings,
        coverage=coverage,
    )


def replay(finding: Finding, rules: Iterable[Rule], schema: FactSchema) -> bool:
    """Re-execute the evidence for ``finding``; True iff it still holds."""
    by_ref = {rule_ref(rule): rule for rule in rules}
    try:
        involved = [by_ref[ref] for ref in finding.rule_refs]
    except KeyError:
        return False

    if finding.kind in ("conflict", "partial_shadow"):
        if finding.counterexample is None:
            return False
        cx = dict(finding.counterexample)
        if set(cx) != set(schema.domains):
            return False
        if any(not schema.domains[f].contains(v) for f, v in cx.items()):
            return False
        first, second = involved
        if not (rule_matches(first, cx) and rule_matches(second, cx)):
            return False
        if finding.kind == "conflict":
            return (
                first.priority == second.priority
                and first.effect != second.effect
            )
        return first.priority > second.priority and not _covers_space(
            first, second, schema
        )

    if finding.kind == "unreachable":
        low, higher = involved[0], involved[1:]
        if not higher or any(h.priority <= low.priority for h in higher):
            return False
        fields = sorted(
            set(low.conditions)
            | {field for h in higher for field in h.conditions}
        )
        constraints = {
            field: low.conditions.get(field, schema.domains[field])
            for field in fields
        }
        for point in _space_points(fields, constraints):
            full = _complete_input(point, schema)
            if not any(rule_matches(h, full) for h in higher):
                return False
        return True

    return False
