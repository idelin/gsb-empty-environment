"""Standalone offline verifier for export packages.

The verifier trusts nothing claimed by the package: it re-checks the
summary digest, validates every reference, and re-derives all root goals
by forward-chaining from the package's own base facts and rules.  A
package is only valid if every goal is derivable, no reference dangles,
every step matches its rule, and the package contains no superfluous or
missing items.

``check_necessity`` additionally verifies minimality in the sense that
removing any single fact, rule or step (and re-signing the summary, so
the failure cannot be attributed to a stale digest) breaks verification.
"""
from __future__ import annotations

import copy
import json
from dataclasses import dataclass, field

from .package import BODY_KEYS, FORMAT, attach_summary, step_id_for, summarize


@dataclass
class VerificationReport:
    ok: bool
    errors: list = field(default_factory=list)
    derived_facts: frozenset = frozenset()
    derived_goals: frozenset = frozenset()


def _load(package):
    if isinstance(package, (bytes, bytearray)):
        package = package.decode("utf-8")
    if isinstance(package, str):
        package = json.loads(package)
    if not isinstance(package, dict):
        raise TypeError("package must be a JSON object")
    return package


def verify_package(package) -> VerificationReport:
    errors: list[str] = []
    try:
        package = _load(package)
    except (TypeError, ValueError, UnicodeDecodeError) as exc:
        return VerificationReport(False, [f"unreadable package: {exc}"])

    if package.get("format") != FORMAT:
        errors.append(f"unsupported format: {package.get('format')!r}")
    missing = [key for key in (*BODY_KEYS, "summary") if key not in package]
    if missing:
        errors.append(f"missing top-level keys: {missing}")
    if errors:
        return VerificationReport(False, errors)

    body = {key: package[key] for key in BODY_KEYS}

    # 1. Summary integrity (digest + counts + public-fact list).
    if package["summary"] != summarize(body):
        errors.append("summary does not match package contents (digest/counts)")

    # 2. Structural validation.
    facts = _index(body["facts"], "fact", ("id", "version", "statement", "public", "base"), errors)
    rules = _index(body["rules"], "rule", ("id", "version", "premises", "conclusion"), errors)
    steps = _index(body["steps"], "step", ("id", "version", "rule", "inputs", "output"), errors)
    goals = body["goals"]
    if not isinstance(goals, list) or not all(isinstance(g, str) for g in goals):
        errors.append("goals must be a list of fact ids")
        goals = []
    if errors:
        return VerificationReport(False, errors)

    # 3. Dangling references.
    for rule in rules.values():
        for premise in rule["premises"]:
            if premise not in facts:
                errors.append(f"dangling reference: rule {rule['id']} premise {premise!r}")
        if rule["conclusion"] not in facts:
            errors.append(
                f"dangling reference: rule {rule['id']} conclusion {rule['conclusion']!r}"
            )
    for step in steps.values():
        if step["rule"] not in rules:
            errors.append(f"dangling reference: step {step['id']} rule {step['rule']!r}")
        for item in step["inputs"]:
            if item not in facts:
                errors.append(f"dangling reference: step {step['id']} input {item!r}")
        if step["output"] not in facts:
            errors.append(f"dangling reference: step {step['id']} output {step['output']!r}")
    for goal in goals:
        if goal not in facts:
            errors.append(f"dangling reference: goal {goal!r} is not in the package")
    if errors:
        return VerificationReport(False, errors)

    # 4. Step/rule consistency and stable ids.
    for step in steps.values():
        rule = rules[step["rule"]]
        if step["id"] != step_id_for(rule["id"]):
            errors.append(f"step {step['id']} does not have the stable id of rule {rule['id']}")
        if step["version"] != rule["version"]:
            errors.append(f"step {step['id']} version does not match its rule version")
        if sorted(step["inputs"]) != sorted(rule["premises"]):
            errors.append(f"step {step['id']} inputs do not match rule {rule['id']} premises")
        if step["output"] != rule["conclusion"]:
            errors.append(f"step {step['id']} output does not match rule {rule['id']} conclusion")
    stepped_rules = {step["rule"] for step in steps.values()}
    for rule_id in rules:
        if rule_id not in stepped_rules:
            errors.append(f"rule {rule_id} has no derivation step")

    # 5. Re-derivation: forward chaining from base facts, ignoring any
    #    claim made by the package about what is proven.
    produced = {step["output"] for step in steps.values()}
    for fact in facts.values():
        if not fact["base"] and fact["id"] not in produced:
            errors.append(f"non-base fact {fact['id']} has no derivation step")
    closure = {fid for fid, fact in facts.items() if fid not in produced}
    changed = True
    while changed:
        changed = False
        for rule in rules.values():
            if rule["conclusion"] in closure:
                continue
            if all(premise in closure for premise in rule["premises"]):
                closure.add(rule["conclusion"])
                changed = True
    for output in produced:
        if output not in closure:
            errors.append(f"step output {output} is not derivable from the package rules")
    derived_goals = set()
    for goal in goals:
        if goal in closure:
            derived_goals.add(goal)
        else:
            errors.append(f"root goal {goal} is not derivable from the package")

    # 6. Tightness: no irrelevant evidence.
    used_as_premise = {premise for rule in rules.values() for premise in rule["premises"]}
    for fact_id in facts:
        if fact_id not in used_as_premise and fact_id not in goals:
            errors.append(f"superfluous fact {fact_id}: neither a goal nor a premise")
    if errors:
        return VerificationReport(False, errors)
    return VerificationReport(True, [], frozenset(closure), frozenset(derived_goals))


def _index(entries, kind, required_keys, errors) -> dict:
    indexed = {}
    if not isinstance(entries, list):
        errors.append(f"{kind}s must be a list")
        return indexed
    for entry in entries:
        if not isinstance(entry, dict) or any(key not in entry for key in required_keys):
            errors.append(f"malformed {kind} entry: {entry!r}")
            continue
        ident = entry["id"]
        if not isinstance(ident, str) or not ident:
            errors.append(f"{kind} has invalid id: {ident!r}")
            continue
        if ident in indexed:
            errors.append(f"duplicate {kind} id: {ident}")
            continue
        if not isinstance(entry["version"], int) or entry["version"] < 1:
            errors.append(f"{kind} {ident} has invalid version")
            continue
        indexed[ident] = entry
    return indexed


@dataclass
class NecessityReport:
    ok: bool
    violations: list = field(default_factory=list)  # items removable without breaking


def check_necessity(package) -> NecessityReport:
    """Remove each item in turn, re-sign, and require verification to fail.

    The summary is recomputed after each removal so a failure can only
    come from the re-derivation / reference checks, never from a stale
    digest.
    """
    package = _load(package)
    body = {key: package[key] for key in BODY_KEYS}
    violations = []

    def still_valid(section, index):
        mutated = copy.deepcopy(body)
        mutated[section].pop(index)
        return verify_package(attach_summary(mutated)).ok

    for index, fact in enumerate(body["facts"]):
        if still_valid("facts", index):
            violations.append(f"fact:{fact['id']}")
    for index, rule in enumerate(body["rules"]):
        if still_valid("rules", index):
            violations.append(f"rule:{rule['id']}")
    for index, step in enumerate(body["steps"]):
        if still_valid("steps", index):
            violations.append(f"step:{step['id']}")
    return NecessityReport(not violations, violations)
