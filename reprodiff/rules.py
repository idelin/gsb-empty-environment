"""Ignore rules and their validation."""

from __future__ import annotations

from dataclasses import dataclass


class RuleError(Exception):
    """Raised when rules overlap or do not match their target type."""


@dataclass(frozen=True)
class JsonKeyRule:
    """Ignore a complete key path inside the JSON document at ``path``.

    Key path elements are object keys (str) or array indices (int).  The
    whole subtree rooted at the key path is ignored; array order and every
    other key still count.
    """

    rule_id: str
    path: str
    key_path: tuple

    def __init__(self, rule_id: str, path: str, key_path) -> None:
        key_path = tuple(key_path)
        if not key_path:
            raise RuleError(f"rule {rule_id!r}: key path must not be empty")
        for elem in key_path:
            if isinstance(elem, bool) or not isinstance(elem, (str, int)):
                raise RuleError(f"rule {rule_id!r}: bad key path element {elem!r}")
            if isinstance(elem, int) and elem < 0:
                raise RuleError(f"rule {rule_id!r}: negative array index {elem}")
        object.__setattr__(self, "rule_id", rule_id)
        object.__setattr__(self, "path", path)
        object.__setattr__(self, "key_path", key_path)

    def to_dict(self) -> dict:
        return {
            "type": "json-key",
            "id": self.rule_id,
            "path": self.path,
            "key_path": list(self.key_path),
        }


@dataclass(frozen=True)
class ZipTimestampRule:
    """Ignore parser-located ``date_time`` metadata of the ZIP at ``path``.

    Only the metadata fields located by the ZIP parser are ignored; payload
    bytes are compared exactly and are never modified.
    """

    rule_id: str
    path: str

    def to_dict(self) -> dict:
        return {"type": "zip-timestamp", "id": self.rule_id, "path": self.path}


def _is_prefix(shorter: tuple, longer: tuple) -> bool:
    return len(shorter) <= len(longer) and longer[: len(shorter)] == shorter


class RuleSet:
    """A validated collection of ignore rules."""

    def __init__(self, rules=()) -> None:
        self._all = list(rules)
        ids = [rule.rule_id for rule in self._all]
        if len(set(ids)) != len(ids):
            raise RuleError("duplicate rule id")
        self._json = {}
        self._zip = {}
        for rule in self._all:
            if isinstance(rule, JsonKeyRule):
                self._json.setdefault(rule.path, []).append(rule)
            elif isinstance(rule, ZipTimestampRule):
                if rule.path in self._zip:
                    raise RuleError(f"overlapping ZIP timestamp rules for {rule.path!r}")
                self._zip[rule.path] = rule
            else:
                raise RuleError(f"unknown rule type: {type(rule).__name__}")
        for path in set(self._json) & set(self._zip):
            raise RuleError(f"path {path!r} has both JSON and ZIP rules")
        for path, group in self._json.items():
            for index, left in enumerate(group):
                for right in group[index + 1:]:
                    if _is_prefix(left.key_path, right.key_path) or _is_prefix(
                        right.key_path, left.key_path
                    ):
                        raise RuleError(
                            f"overlapping JSON rules {left.rule_id!r} and "
                            f"{right.rule_id!r} for {path!r}"
                        )

    def json_rules_for(self, path: str):
        return self._json.get(path, ())

    def zip_rule_for(self, path: str):
        return self._zip.get(path)

    def __iter__(self):
        return iter(self._all)

    def to_list(self) -> list:
        return sorted((rule.to_dict() for rule in self._all), key=lambda item: item["id"])

    @classmethod
    def from_list(cls, items) -> "RuleSet":
        rules = []
        for item in items:
            if item["type"] == "json-key":
                rules.append(JsonKeyRule(item["id"], item["path"], tuple(item["key_path"])))
            elif item["type"] == "zip-timestamp":
                rules.append(ZipTimestampRule(item["id"], item["path"]))
            else:
                raise RuleError(f"unknown rule type {item['type']!r}")
        return cls(rules)
