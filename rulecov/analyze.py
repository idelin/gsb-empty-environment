"""Rule-coverage analysis with a bounded operation budget.

Three analyses run in fixed order, sharing one budget:

1. coverage  -- a rule whose condition region is fully covered by the union
   of strictly-higher-priority rules is *unreachable*.
2. overlap   -- a rule only partially covered by a higher-priority rule
   yields a concrete counterexample input from the overlapping region.
3. conflict  -- two same-priority rules with different actions whose
   regions overlap yield a concrete counterexample input.

Every set operation is recorded as a step with a deterministic id
(S0001, S0002, ...) and consumes one unit of budget.  If the budget is
exhausted the affected phases are reported as "unknown"; the analyzer
never claims the absence of conflicts it did not check.
"""
from __future__ import annotations

import hashlib
import json

from .regions import (
    EnumSet,
    intersect_regions,
    is_disjoint,
    region_to_json,
    subtract_region,
)

GENERATOR = {"name": "rulecov", "version": "1.0.0"}
PHASES = ("coverage", "overlap", "conflict")


class BudgetExhausted(Exception):
    pass


class Recorder:
    """Records one step per set operation; enforces the budget."""

    def __init__(self, limit):
        self.limit = limit
        self.steps = []

    def record(self, op, detail):
        if len(self.steps) >= self.limit:
            raise BudgetExhausted(
                f"budget of {self.limit} operations exhausted")
        step_id = f"S{len(self.steps) + 1:04d}"
        self.steps.append({"step_id": step_id, "op": op, **detail})
        return step_id


def _witness(region, spec):
    """Pick a deterministic concrete input inside ``region``."""
    inputs = {}
    for fact in spec.facts:
        constraint = region.get(fact.fact_id, fact.domain())
        if isinstance(constraint, EnumSet):
            inputs[fact.fact_id] = sorted(constraint.values)[0]
        else:
            inputs[fact.fact_id] = constraint.lo
    return inputs


def _coverage(spec, rule, higher_rules, recorder):
    """Subtract higher-priority regions from ``rule``'s region.

    Returns (fully_covered, remainder_regions, step_ids).
    """
    domain = spec.domain()
    worklist = [dict(rule.conditions)]
    step_ids = []
    for other in higher_rules:
        box = other.conditions
        next_worklist = []
        for region in worklist:
            if is_disjoint(region, box):
                step_ids.append(recorder.record("intersect", {
                    "phase": "coverage", "rule": rule.rule_id,
                    "against_rule": other.rule_id, "empty": True}))
                next_worklist.append(region)
                continue
            remainder = subtract_region(region, box, domain)
            step_ids.append(recorder.record("subtract", {
                "phase": "coverage", "rule": rule.rule_id,
                "against_rule": other.rule_id,
                "remainder_regions": len(remainder)}))
            next_worklist.extend(remainder)
        worklist = next_worklist
        if not worklist:
            return True, [], step_ids
    return False, worklist, step_ids


def _phase_coverage(spec, recorder, findings):
    unreachable = {}
    for rule in sorted(spec.rules, key=lambda r: r.rule_id):
        higher = sorted(
            (r for r in spec.rules if r.priority > rule.priority),
            key=lambda r: (-r.priority, r.rule_id))
        covered, _remainder, step_ids = _coverage(spec, rule, higher, recorder)
        if covered:
            unreachable[rule.rule_id] = rule
            findings.append({
                "finding_id": f"F-UNREACHABLE-{rule.rule_id}",
                "kind": "unreachable_rule",
                "rule": rule.ref(),
                "covered_by": [r.ref() for r in higher],
                "evidence": {"steps": step_ids},
            })
    return unreachable


def _phase_overlap(spec, recorder, findings, unreachable):
    rules = sorted(spec.rules, key=lambda r: r.rule_id)
    for low in rules:
        if low.rule_id in unreachable:
            continue  # already reported as unreachable; overlap is subsumed
        for high in rules:
            if high.priority <= low.priority:
                continue
            inter = intersect_regions(low.conditions, high.conditions)
            step_id = recorder.record("intersect", {
                "phase": "overlap", "rule": low.rule_id,
                "against_rule": high.rule_id, "empty": inter is None})
            if inter is None:
                continue
            witness = _witness(inter, spec)
            findings.append({
                "finding_id": f"F-OVERLAP-{low.rule_id}-{high.rule_id}",
                "kind": "partial_overlap",
                "low_rule": low.ref(),
                "high_rule": high.ref(),
                "overlap_region": region_to_json(inter),
                "counterexample": {
                    "input": witness,
                    "winner": high.rule_id,
                    "loser": low.rule_id,
                },
                "evidence": {"steps": [step_id]},
            })


def _phase_conflict(spec, recorder, findings):
    rules = sorted(spec.rules, key=lambda r: r.rule_id)
    for i, left in enumerate(rules):
        for right in rules[i + 1:]:
            if right.priority != left.priority:
                continue
            if left.action == right.action:
                continue  # same action: redundant, not a conflict
            inter = intersect_regions(left.conditions, right.conditions)
            step_id = recorder.record("intersect", {
                "phase": "conflict", "rule": left.rule_id,
                "against_rule": right.rule_id, "empty": inter is None})
            if inter is None:
                continue
            witness = _witness(inter, spec)
            findings.append({
                "finding_id": f"F-CONFLICT-{left.rule_id}-{right.rule_id}",
                "kind": "same_priority_conflict",
                "rules": [left.ref(), right.ref()],
                "priority": left.priority,
                "actions": sorted({left.action, right.action}),
                "overlap_region": region_to_json(inter),
                "counterexample": {
                    "input": witness,
                    "matches": [left.rule_id, right.rule_id],
                },
                "evidence": {"steps": [step_id]},
            })


def _canonical(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"),
                      ensure_ascii=False)


def analyze(spec, budget_limit=100000):
    """Run all analyses; returns a deterministic, replayable report dict."""
    recorder = Recorder(budget_limit)
    findings = []
    phase_status = {}
    exhausted = False
    unreachable = {}
    for phase in PHASES:
        if exhausted:
            phase_status[phase] = "unknown"
            continue
        try:
            if phase == "coverage":
                unreachable = _phase_coverage(spec, recorder, findings)
            elif phase == "overlap":
                _phase_overlap(spec, recorder, findings, unreachable)
            else:
                _phase_conflict(spec, recorder, findings)
            phase_status[phase] = "complete"
        except BudgetExhausted:
            exhausted = True
            phase_status[phase] = "unknown"

    spec_doc = spec.to_doc()
    report = {
        "spec": {
            "id": spec.spec_id,
            "version": spec.version,
            "sha256": hashlib.sha256(_canonical(spec_doc).encode()).hexdigest(),
        },
        "generator": GENERATOR,
        "budget": {
            "limit": budget_limit,
            "used": len(recorder.steps),
            "exhausted": exhausted,
        },
        "phases": phase_status,
        "complete": not exhausted,
        "findings": findings,
        "steps": recorder.steps,
    }
    digest = hashlib.sha256(_canonical(report).encode()).hexdigest()
    report["report_id"] = f"RPT-{digest[:16]}"
    return report
