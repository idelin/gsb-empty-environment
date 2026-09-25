"""Independent offline verifier for exported packages.

The verifier trusts nothing declared in the package: it re-derives every
conclusion by forward-chaining from the package's own base facts and
rules, then requires the declared proof steps to match that replay
exactly.  It also enforces referential integrity (no dangling
references), the content hash, and relevance (no unused evidence).
"""

from __future__ import annotations

import copy
import hashlib

from .export import FORMAT, canonical_bytes, resign


class VerificationError(Exception):
    """Raised whenever a package fails any verification check."""


def _fail(message: str) -> None:
    raise VerificationError(message)


def _check_summary(pkg: dict) -> None:
    summary = pkg["summary"]
    stored = summary.get("sha256")
    if not stored:
        _fail("summary.sha256 missing")
    unsigned = copy.deepcopy(pkg)
    unsigned["summary"] = {k: v for k, v in summary.items() if k != "sha256"}
    actual = hashlib.sha256(canonical_bytes(unsigned)).hexdigest()
    if actual != stored:
        _fail("content hash mismatch (tampered or corrupt package)")
    facts, rules, steps = pkg["facts"], pkg["rules"], pkg["steps"]
    expected = {
        "facts": len(facts),
        "base_facts": sum(1 for f in facts if f.get("role") == "base"),
        "public_base_facts": sum(
            1 for f in facts if f.get("role") == "base" and f.get("public")
        ),
        "rules": len(rules),
        "steps": len(steps),
        "package_items": len(facts) + len(rules) + len(steps),
    }
    for key, value in expected.items():
        if summary.get(key) != value:
            _fail(f"summary.{key} mismatch: {summary.get(key)} != {value}")


def _check_schema(pkg: dict) -> None:
    for key in ("goals", "facts", "rules", "steps", "summary"):
        if key not in pkg:
            _fail(f"missing section: {key}")
    for fact in pkg["facts"]:
        for key in ("id", "version", "public", "role", "payload"):
            if key not in fact:
                _fail(f"fact missing field: {key}")
        if fact["role"] not in ("base", "derived"):
            _fail(f"fact {fact['id']} has invalid role {fact['role']!r}")
    for rule in pkg["rules"]:
        for key in ("id", "version", "premises", "conclusion"):
            if key not in rule:
                _fail(f"rule missing field: {key}")
    for step in pkg["steps"]:
        for key in ("id", "version", "rule", "rule_version", "inputs", "output"):
            if key not in step:
                _fail(f"step missing field: {key}")
    for section in ("facts", "rules", "steps"):
        ids = [item["id"] for item in pkg[section]]
        if len(ids) != len(set(ids)):
            _fail(f"duplicate IDs in section: {section}")


def verify_package(pkg: dict) -> dict:
    """Fully verify a package offline; return a report on success."""

    if not isinstance(pkg, dict):
        _fail("package must be a JSON object")
    if pkg.get("format") != FORMAT:
        _fail(f"unsupported format: {pkg.get('format')!r}")
    _check_schema(pkg)
    _check_summary(pkg)

    facts = pkg["facts"]
    rules = pkg["rules"]
    steps = pkg["steps"]
    goals = pkg["goals"]
    fact_ids = {f["id"] for f in facts}
    rules_by_id = {r["id"]: r for r in rules}

    # Referential integrity: no dangling references anywhere.
    for rule in rules:
        for ref in (*rule["premises"], rule["conclusion"]):
            if ref not in fact_ids:
                _fail(f"dangling reference: rule {rule['id']} -> {ref}")
    for step in steps:
        if step["rule"] not in rules_by_id:
            _fail(f"dangling reference: step {step['id']} -> {step['rule']}")
    for goal in goals:
        if goal not in fact_ids:
            _fail(f"dangling reference: goal {goal}")

    # Independent replay: forward-chain from base facts using only the
    # package's own rules, in deterministic rule-ID order.
    base = {f["id"] for f in facts if f["role"] == "base"}
    available = set(base)
    expected_steps = []
    pending = sorted(rules_by_id)
    while pending:
        progressed = False
        for rule_id in list(pending):
            rule = rules_by_id[rule_id]
            if all(p in available for p in rule["premises"]):
                expected_steps.append(
                    {
                        "id": f"S{len(expected_steps) + 1:03d}",
                        "version": 1,
                        "rule": rule["id"],
                        "rule_version": rule["version"],
                        "inputs": sorted(rule["premises"]),
                        "output": rule["conclusion"],
                    }
                )
                available.add(rule["conclusion"])
                pending.remove(rule_id)
                progressed = True
        if not progressed:
            _fail("rules cannot be replayed (dangling premise or cycle)")
    if expected_steps != steps:
        _fail("declared steps do not match independent replay")

    derived_declared = {f["id"] for f in facts if f["role"] == "derived"}
    derived_actual = {s["output"] for s in steps}
    if derived_declared != derived_actual:
        _fail("derived facts do not match replay outputs")

    missing = [g for g in goals if g not in available]
    if missing:
        _fail(f"goals not derivable from package: {missing}")

    # Relevance: no unused evidence or rules may ride along.
    used = set(goals)
    for rule in rules:
        used.update(rule["premises"])
    unused = sorted(f for f in base if f not in used)
    if unused:
        _fail(f"irrelevant evidence in package: {unused}")

    return {
        "ok": True,
        "goals": sorted(goals),
        "derived": sorted(available),
        "steps": len(steps),
        "package_items": pkg["summary"]["package_items"],
        "sha256": pkg["summary"]["sha256"],
    }


def minimality_failures(pkg: dict) -> list[str]:
    """Items whose removal still verifies (an empty list means minimal).

    Every fact, rule and step is removed in turn; the mutant is re-signed
    so only genuine semantic necessity is tested.
    """

    failures: list[str] = []

    def survives(mutant: dict) -> bool:
        try:
            verify_package(resign(mutant))
        except VerificationError:
            return False
        return True

    for index, fact in enumerate(pkg["facts"]):
        mutant = copy.deepcopy(pkg)
        del mutant["facts"][index]
        if survives(mutant):
            failures.append(f"fact {fact['id']}")
    for index, rule in enumerate(pkg["rules"]):
        mutant = copy.deepcopy(pkg)
        del mutant["rules"][index]
        if survives(mutant):
            failures.append(f"rule {rule['id']}")
    for index, step in enumerate(pkg["steps"]):
        mutant = copy.deepcopy(pkg)
        del mutant["steps"][index]
        if survives(mutant):
            failures.append(f"step {step['id']}")
    return failures
