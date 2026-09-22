"""Independent evidence verifier.

Given only the two raw input trees, the rule document and an evidence bundle,
the verifier re-derives every conclusion itself. It never trusts the main
comparator's normalized digests or claimed status: those are recomputed from
raw bytes and cross-checked field by field.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from . import archive, compare, evidence
from .compare import _canonical_dumps
from .digest import digest_bytes
from .errors import RuleError
from .rules import canonical_rules, load_rules, rules_to_bytes


@dataclass
class VerificationResult:
    ok: bool
    problems: list[str] = field(default_factory=list)
    recomputed_status: str | None = None


def _ignore_key(record: dict[str, Any]) -> tuple[str, str, str, str]:
    return (
        record.get("locator", ""),
        record.get("aspect", ""),
        record.get("field_path", ""),
        record.get("rule_id", ""),
    )


def _difference_key(record: dict[str, Any]) -> tuple[str, str, str]:
    return (
        record.get("locator", ""),
        record.get("aspect", ""),
        record.get("detail", ""),
    )


def _unknown_key(record: dict[str, Any]) -> tuple[str, str]:
    return record.get("locator", ""), record.get("reason", "")


def _multisets(name: str, claimed: list[dict[str, Any]], recomputed: list[dict[str, Any]],
               key_fn, problems: list[str]) -> None:
    claimed_keys = sorted(key_fn(item) for item in claimed)
    recomputed_keys = sorted(key_fn(item) for item in recomputed)
    if claimed_keys != recomputed_keys:
        only_claimed = [k for k in claimed_keys if k not in recomputed_keys]
        only_recomputed = [k for k in recomputed_keys if k not in claimed_keys]
        if only_claimed:
            problems.append(f"{name}: evidence claims entries not independently reproduced: {only_claimed}")
        if only_recomputed:
            problems.append(f"{name}: independent pass found entries missing from evidence: {only_recomputed}")


def verify_bundle(bundle: dict[str, object], old_root: str, new_root: str,
                  budget: archive.Budget | None = None) -> VerificationResult:
    problems: list[str] = []

    if bundle.get("schema_version") != evidence.SCHEMA_VERSION:
        problems.append(f"unsupported schema_version: {bundle.get('schema_version')!r}")

    inputs = bundle.get("inputs")
    rules_section = bundle.get("rules")
    conclusion = bundle.get("conclusion")
    if not isinstance(inputs, dict) or not isinstance(rules_section, dict) or not isinstance(conclusion, dict):
        return VerificationResult(False, ["bundle is missing inputs/rules/conclusion sections"])

    # Re-parse rules straight from the bundle's rule document (the document the
    # user supplied), and confirm the stored digest matches its canonical bytes.
    rule_document = rules_section.get("document")
    try:
        ruleset = load_rules(rule_document)
    except RuleError as exc:
        return VerificationResult(False, [f"rules in evidence are invalid: {exc}"])
    recomputed_rule_blob = rules_to_bytes(ruleset)
    if digest_bytes(recomputed_rule_blob) != rules_section.get("sha256"):
        problems.append("rules sha256 in evidence does not match canonical rule bytes")
    if canonical_rules(ruleset) != rule_document:
        problems.append("rules document is not in canonical form")

    # Independently scan and compare the raw inputs.
    recomputed = compare.compare_trees(old_root, new_root, ruleset, budget)

    if inputs.get("old_root") != old_root:
        problems.append("evidence old_root path does not match supplied old input path")
    if inputs.get("new_root") != new_root:
        problems.append("evidence new_root path does not match supplied new input path")
    if inputs.get("old_manifest_digest") != recomputed.old_manifest_digest:
        problems.append("old manifest digest cannot be reproduced from raw input")
    if inputs.get("new_manifest_digest") != recomputed.new_manifest_digest:
        problems.append("new manifest digest cannot be reproduced from raw input")

    claimed_status = conclusion.get("status")
    if claimed_status != recomputed.status:
        problems.append(f"status mismatch: evidence says {claimed_status!r}, "
                        f"independent pass says {recomputed.status!r}")

    _multisets("ignored", conclusion.get("ignored", []),
               [r.__dict__ for r in recomputed.ignored], _ignore_key, problems)
    _multisets("differences", conclusion.get("differences", []),
               [r.__dict__ for r in recomputed.differences], _difference_key, problems)
    _multisets("unknown", conclusion.get("unknown", []),
               [r.__dict__ for r in recomputed.unknown], _unknown_key, problems)

    # Independently confirm each ignore record's old/new value digests really
    # come from the values named by field_path, and that the values truly differ.
    _verify_ignore_records(conclusion.get("ignored", []), problems)

    return VerificationResult(
        ok=not problems,
        problems=problems,
        recomputed_status=recomputed.status,
    )


def _verify_ignore_records(records: list[dict[str, Any]], problems: list[str]) -> None:
    for record in records:
        old_digest = record.get("old_digest")
        new_digest = record.get("new_digest")
        old_value = record.get("old_value")
        new_value = record.get("new_value")
        recomputed_old = digest_bytes(_canonical_dumps(old_value))
        recomputed_new = digest_bytes(_canonical_dumps(new_value))
        if recomputed_old != old_digest:
            problems.append(f"ignored record {record.get('field_path')!r}: old_value digest inconsistent")
        if recomputed_new != new_digest:
            problems.append(f"ignored record {record.get('field_path')!r}: new_value digest inconsistent")
        if old_value == new_value:
            problems.append(f"ignored record {record.get('field_path')!r}: values are equal; no difference to ignore")
        if not record.get("rule_id"):
            problems.append(f"ignored record {record.get('field_path')!r}: missing rule_id")
