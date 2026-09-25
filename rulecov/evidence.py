"""Replayable evidence verification.

``verify_report`` re-executes the whole analysis with the budget recorded
in the report and compares the canonical result, then independently checks
every counterexample witness by concrete evaluation.  A report only
verifies if both the deterministic replay and every witness check pass.
"""
from __future__ import annotations

from .analyze import analyze
from .model import evaluate


def _check_witness(spec, report, finding, problems):
    fid = finding["finding_id"]
    kind = finding["kind"]
    rules = spec.rule_map()

    if kind == "unreachable_rule":
        rule = rules[finding["rule"]["id"]]
        for ref in finding["covered_by"]:
            if rules[ref["id"]].priority <= rule.priority:
                problems.append(
                    f"{fid}: covering rule {ref['id']} is not strictly "
                    f"higher priority than {rule.rule_id}")
        return

    witness = finding["counterexample"]["input"]
    try:
        matched = evaluate(spec, witness)
    except Exception as exc:  # input outside domain etc.
        problems.append(f"{fid}: witness input rejected: {exc}")
        return
    matched_ids = [r.rule_id for r in matched]

    if kind == "partial_overlap":
        low = rules[finding["low_rule"]["id"]]
        high = rules[finding["high_rule"]["id"]]
        if low.rule_id not in matched_ids or high.rule_id not in matched_ids:
            problems.append(f"{fid}: witness does not match both rules")
        if high.priority <= low.priority:
            problems.append(f"{fid}: high_rule is not strictly higher priority")
        if matched and matched[0].priority <= low.priority:
            problems.append(f"{fid}: witness winner does not outrank low_rule")
    elif kind == "same_priority_conflict":
        left, right = (rules[r["id"]] for r in finding["rules"])
        if left.rule_id not in matched_ids or right.rule_id not in matched_ids:
            problems.append(f"{fid}: witness does not match both rules")
        if left.priority != right.priority:
            problems.append(f"{fid}: rules do not share a priority")
        if left.action == right.action:
            problems.append(f"{fid}: rules share an action; not a conflict")
        top = [r for r in matched if r.priority == matched[0].priority]
        if len({r.action for r in top}) < 2:
            problems.append(f"{fid}: witness does not produce an ambiguous winner")
    else:
        problems.append(f"{fid}: unknown finding kind {kind!r}")


def verify_report(spec, report):
    """Return a list of problems; empty list means the report replays."""
    problems = []

    expected = analyze(spec, report["budget"]["limit"])
    if expected != report:
        problems.append(
            "replay mismatch: re-running the analysis with the recorded "
            "budget produces a different report")
        return problems  # witness checks would be meaningless

    if report["budget"]["exhausted"] and report["complete"]:
        problems.append("budget exhausted but report claims completeness")
    if report["budget"]["exhausted"] and report["phases"].get("conflict") == "complete":
        problems.append("budget exhausted but conflict phase claims completeness")

    for finding in report["findings"]:
        _check_witness(spec, report, finding, problems)
    return problems
