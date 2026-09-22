"""Ignore-rule model and validation.

Rules can only ignore:

* full JSON key paths (user supplied);
* ZIP timestamp metadata fields located by the parser (built-in).

Overlapping rules and type mismatches are errors, never silent merges.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import PurePosixPath

from .errors import RuleError

ZIP_TIMESTAMP_RULE_ID = "builtin:zip-timestamp:v1"

_JSON_TYPES = frozenset(
    {"any", "object", "array", "string", "integer", "number", "bool", "null", "scalar"}
)


@dataclass(frozen=True)
class JsonIgnoreRule:
    rule_id: str
    target: str
    key_path: tuple[str, ...]
    expected_type: str = "any"

    def locator(self) -> tuple[str, ...]:
        return parse_locator(self.target)

    def key_path_json(self) -> list[str]:
        return list(self.key_path)


@dataclass(frozen=True)
class RuleSet:
    json_rules: tuple[JsonIgnoreRule, ...] = ()
    ignore_zip_timestamps: bool = True

    def by_locator(self) -> dict[tuple[str, ...], tuple[JsonIgnoreRule, ...]]:
        index: dict[tuple[str, ...], list[JsonIgnoreRule]] = {}
        for rule in self.json_rules:
            index.setdefault(rule.locator(), []).append(rule)
        return {loc: tuple(rules) for loc, rules in index.items()}


def parse_locator(target: str) -> tuple[str, ...]:
    if not isinstance(target, str) or not target:
        raise RuleError("rule target must be a non-empty string")
    if "\x00" in target:
        raise RuleError(f"NUL byte in rule target: {target!r}")
    parts = target.split("!/")
    if any(part == "" for part in parts):
        raise RuleError(f"empty path segment in rule target: {target!r}")
    for part in parts:
        if part.startswith("/"):
            raise RuleError(f"absolute segment not allowed in target: {target!r}")
        normalized = PurePosixPath(part)
        if normalized.is_absolute() or ".." in normalized.parts or "." in normalized.parts:
            raise RuleError(f"normalized path component not allowed: {target!r}")
    return tuple(parts)


def _coerce_key_path(raw: object, rule_id: str) -> tuple[str, ...]:
    if not isinstance(raw, list) or not raw:
        raise RuleError(f"rule {rule_id!r}: key_path must be a non-empty list of keys")
    keys: list[str] = []
    for item in raw:
        if not isinstance(item, str) or item == "":
            raise RuleError(f"rule {rule_id!r}: key_path entries must be non-empty strings")
        keys.append(item)
    return tuple(keys)


def _paths_overlap(a: tuple[str, ...], b: tuple[str, ...]) -> bool:
    short, long_ = (a, b) if len(a) <= len(b) else (b, a)
    return long_[: len(short)] == short


def _load_rule(raw: object) -> JsonIgnoreRule:
    if not isinstance(raw, dict):
        raise RuleError("each rule must be a JSON object")
    rule_id = raw.get("rule_id")
    if not isinstance(rule_id, str) or not rule_id:
        raise RuleError("rule is missing a non-empty rule_id")
    if rule_id == ZIP_TIMESTAMP_RULE_ID:
        raise RuleError(f"rule_id {rule_id!r} is reserved for built-in ZIP timestamp ignores")
    target = raw.get("target")
    if not isinstance(target, str):
        raise RuleError(f"rule {rule_id!r}: target must be a string")
    key_path = _coerce_key_path(raw.get("key_path"), rule_id)
    expected_type = raw.get("expected_type", "any")
    if not isinstance(expected_type, str) or expected_type not in _JSON_TYPES:
        raise RuleError(
            f"rule {rule_id!r}: expected_type must be one of {sorted(_JSON_TYPES)}"
        )
    return JsonIgnoreRule(rule_id=rule_id, target=target, key_path=key_path,
                          expected_type=expected_type)


def load_rules(raw: object) -> RuleSet:
    if raw is None:
        return RuleSet()
    if not isinstance(raw, dict):
        raise RuleError("rules document must be a JSON object")
    rules_raw = raw.get("json_key_ignores", [])
    if not isinstance(rules_raw, list):
        raise RuleError("json_key_ignores must be a list")
    rules = tuple(_load_rule(item) for item in rules_raw)

    seen: set[tuple[str, tuple[str, ...]]] = set()
    for rule in rules:
        loc = rule.locator()
        signature = (loc, rule.key_path)
        if signature in seen:
            raise RuleError(
                f"duplicate rules for {rule.target} key path {list(rule.key_path)}"
            )
        seen.add(signature)

    grouped: dict[tuple[str, ...], list[JsonIgnoreRule]] = {}
    for rule in rules:
        grouped.setdefault(rule.locator(), []).append(rule)
    for loc, group in grouped.items():
        for i, first in enumerate(group):
            for second in group[i + 1:]:
                if _paths_overlap(first.key_path, second.key_path):
                    raise RuleError(
                        "overlapping rules "
                        f"{first.rule_id!r} ({list(first.key_path)}) and "
                        f"{second.rule_id!r} ({list(second.key_path)}) on target {loc}"
                    )

    zip_ts = raw.get("ignore_zip_timestamps", True)
    if not isinstance(zip_ts, bool):
        raise RuleError("ignore_zip_timestamps must be a boolean")
    return RuleSet(json_rules=rules, ignore_zip_timestamps=zip_ts)


def canonical_rules(ruleset: RuleSet) -> dict[str, object]:
    return {
        "ignore_zip_timestamps": ruleset.ignore_zip_timestamps,
        "json_key_ignores": [
            {
                "rule_id": rule.rule_id,
                "target": rule.target,
                "key_path": rule.key_path_json(),
                "expected_type": rule.expected_type,
            }
            for rule in ruleset.json_rules
        ],
    }


def rules_to_bytes(ruleset: RuleSet) -> bytes:
    return (json.dumps(canonical_rules(ruleset), ensure_ascii=False,
                       sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
