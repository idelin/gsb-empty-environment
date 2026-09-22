"""Main comparator for directory trees and their nested ZIP archives.

Outcomes per compared item:

* ``equal``        - originals are identical, no rule applied;
* ``equivalent``   - identical except for differences explicitly permitted
                     by validated ignore rules (evidence retained);
* ``different``    - substantive difference;
* ``undeterminable`` - a malformed container, traversal, duplicate member,
                     budget exhaustion or rule error prevents a verdict.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from decimal import Decimal
from typing import Any

from . import archive, jsonutil
from .digest import digest_bytes
from .errors import CorruptArchiveError, QuotaExceededError
from .rules import ZIP_TIMESTAMP_RULE_ID, JsonIgnoreRule, RuleSet
from .tree import scan_tree

EQUAL = "equal"
EQUIVALENT = "equivalent"
DIFFERENT = "different"
UNDETERMINABLE = "undeterminable"

# Undeterminable reason codes
REASON_CORRUPT = "corrupt-archive"
REASON_TRAVERSAL = "path-traversal"
REASON_DUPLICATE_MEMBER = "duplicate-zip-member"
REASON_BUDGET = "budget-exhausted"
REASON_JSON_DUPLICATE_KEY = "json-duplicate-key"
REASON_JSON_PARSE = "json-parse-error"
REASON_RULE_TARGET_MISSING = "rule-target-missing"
REASON_RULE_TARGET_TYPE = "rule-target-type-mismatch"
REASON_TREE_READ = "tree-read-error"
REASON_RULE_TARGET_NOT_JSON = "rule-target-not-json"


@dataclass
class IgnoreRecord:
    locator: str
    aspect: str
    field_path: str
    old_digest: str
    new_digest: str
    old_value: Any
    new_value: Any
    rule_id: str


@dataclass
class Difference:
    locator: str
    aspect: str
    detail: str
    old_digest: str | None = None
    new_digest: str | None = None


@dataclass
class Unknown:
    locator: str
    reason: str
    detail: str


@dataclass
class ComparisonResult:
    status: str
    old_root: str
    new_root: str
    old_manifest_digest: str
    new_manifest_digest: str
    ignored: list[IgnoreRecord] = field(default_factory=list)
    differences: list[Difference] = field(default_factory=list)
    unknown: list[Unknown] = field(default_factory=list)
    rule_errors: list[str] = field(default_factory=list)

    def to_evidence(self) -> dict[str, Any]:
        return {
            "schema_version": 1,
            "status": self.status,
            "old_root": self.old_root,
            "new_root": self.new_root,
            "old_manifest_digest": self.old_manifest_digest,
            "new_manifest_digest": self.new_manifest_digest,
            "ignored": [record.__dict__ for record in self.ignored],
            "differences": [d.__dict__ for d in self.differences],
            "unknown": [u.__dict__ for u in self.unknown],
        }


def _manifest_digest(entries: dict[str, TreeEntry]) -> str:
    parts: list[str] = []
    for rel in sorted(entries):
        entry = entries[rel]
        parts.append(
            "\x1f".join(
                [
                    rel,
                    entry.kind,
                    oct(entry.mode),
                    entry.symlink_target if entry.symlink_target is not None else "",
                    digest_bytes(entry.data),
                ]
            )
        )
    return digest_bytes("\x1e".join(parts).encode("utf-8", "surrogateescape"))


def _locator(segments: list[str]) -> str:
    return "!/".join(segments)


def _canonical_dumps(value: Any) -> bytes:
    return __import__("json").dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        default=_json_default,
    ).encode("utf-8")


def _json_default(obj):
    if isinstance(obj, Decimal):
        return {"__decimal__": format(obj, "f")}
    raise TypeError(f"cannot serialize {type(obj)!r}")


def _json_value_digest(value: Any) -> str:
    return digest_bytes(_canonical_dumps(jsonutil.canonical_value(value)))


def _build_ignore_trie(rules: tuple[JsonIgnoreRule, ...]):
    root: dict[str, Any] = {}
    terminal: dict[tuple[str, ...], JsonIgnoreRule] = {}
    for rule in rules:
        node = root
        for key in rule.key_path:
            node = node.setdefault(key, {})
            if not isinstance(node, dict):
                raise RuleError(
                    f"rule {rule.rule_id!r}: key path collides with a shorter rule"
                )
        node["$"] = jsonutil._IGNORED_SENTINEL
        terminal[rule.key_path] = rule
    return root, terminal


def _check_rule_targets(root_value: Any, rules: tuple[JsonIgnoreRule, ...],
                       locator: str, unknown: list[Unknown], base_segments: list[str]) -> None:
    for rule in rules:
        found, value = jsonutil.resolve_path(root_value, rule.key_path)
        field_path = locator + "#" + "/".join(rule.key_path)
        if not found:
            unknown.append(Unknown(field_path, REASON_RULE_TARGET_MISSING,
                                   f"rule {rule.rule_id} target key not present"))
        elif not jsonutil.type_matches(rule.expected_type, value):
            unknown.append(
                Unknown(
                    field_path,
                    REASON_RULE_TARGET_TYPE,
                    f"rule {rule.rule_id} expected {rule.expected_type}, "
                    f"found {jsonutil.type_name(value)}",
                )
            )


def compare_json_payload(old_bytes: bytes, new_bytes: bytes, rules: tuple[JsonIgnoreRule, ...],
                         segments: list[str], ignored: list[IgnoreRecord],
                         differences: list[Difference], unknown: list[Unknown]) -> str:
    locator = _locator(segments)
    try:
        old_value = jsonutil.parse_json_bytes(old_bytes)
    except jsonutil.DuplicateKeyError as exc:
        unknown.append(Unknown(locator, REASON_JSON_DUPLICATE_KEY, str(exc)))
        return UNDETERMINABLE
    except jsonutil.JSONDecodeError as exc:
        unknown.append(Unknown(locator, REASON_JSON_PARSE, str(exc)))
        return UNDETERMINABLE
    try:
        new_value = jsonutil.parse_json_bytes(new_bytes)
    except jsonutil.DuplicateKeyError as exc:
        unknown.append(Unknown(locator, REASON_JSON_DUPLICATE_KEY, str(exc)))
        return UNDETERMINABLE
    except jsonutil.JSONDecodeError as exc:
        unknown.append(Unknown(locator, REASON_JSON_PARSE, str(exc)))
        return UNDETERMINABLE

    _check_rule_targets(old_value, rules, locator, unknown, segments)
    _check_rule_targets(new_value, rules, locator, unknown, segments)

    trie, terminal = _build_ignore_trie(rules)
    for key_path, rule in terminal.items():
        old_found, old_target = jsonutil.resolve_path(old_value, key_path)
        new_found, new_target = jsonutil.resolve_path(new_value, key_path)
        if old_found and new_found:
            if jsonutil.canonical_value(old_target) != jsonutil.canonical_value(new_target):
                ignored.append(
                    IgnoreRecord(
                        locator=locator,
                        aspect="json-key",
                        field_path=locator + "#" + "/".join(key_path),
                        old_digest=_json_value_digest(old_target),
                        new_digest=_json_value_digest(new_target),
                        old_value=jsonutil.canonical_value(old_target),
                        new_value=jsonutil.canonical_value(new_target),
                        rule_id=rule.rule_id,
                    )
                )

    local_reasons = {
        REASON_RULE_TARGET_MISSING, REASON_RULE_TARGET_TYPE,
        REASON_JSON_DUPLICATE_KEY, REASON_JSON_PARSE,
    }
    def _is_local(entry):
        if entry.reason not in local_reasons:
            return False
        return entry.locator == locator or entry.locator.startswith(locator + "#")
    if any(_is_local(u) for u in unknown):
        return UNDETERMINABLE

    old_residual = jsonutil.residual(old_value, trie)
    new_residual = jsonutil.residual(new_value, trie)
    if old_residual != new_residual:
        differences.append(
            Difference(
                locator=locator,
                aspect="json-content",
                detail="JSON payload differs outside ignored key paths",
                old_digest=digest_bytes(old_bytes),
                new_digest=digest_bytes(new_bytes),
            )
        )
        return DIFFERENT
    return EQUIVALENT if any(i.locator == locator for i in ignored) else EQUAL


def _archive_error_to_unknown(exc: Exception, locator: str, unknown: list[Unknown]) -> None:
    if isinstance(exc, CorruptArchiveError):
        message = str(exc)
        if "duplicate ZIP member name" in message:
            reason = REASON_DUPLICATE_MEMBER
        elif "traversal" in message:
            reason = REASON_TRAVERSAL
        else:
            reason = REASON_CORRUPT
        unknown.append(Unknown(locator, reason, message))
    elif isinstance(exc, QuotaExceededError):
        unknown.append(Unknown(locator, REASON_BUDGET, exc.reason))
    else:  # pragma: no cover - defensive
        unknown.append(Unknown(locator, REASON_CORRUPT, repr(exc)))


def _compare_mode(old_mode: int, new_mode: int, locator: str,
                  differences: list[Difference]) -> bool:
    if old_mode != new_mode:
        differences.append(
            Difference(
                locator=locator,
                aspect="mode",
                detail=f"permission bits differ: {oct(old_mode)} vs {oct(new_mode)}",
            )
        )
        return True
    return False


def _compare_payload(old_bytes: bytes, new_bytes: bytes, segments: list[str],
                     rules_by_locator: dict[tuple[str, ...], tuple[JsonIgnoreRule, ...]],
                     ruleset: RuleSet, budget: archive.Budget,
                     ignored: list[IgnoreRecord], differences: list[Difference],
                     unknown: list[Unknown], matched_rule_locators: set[tuple[str, ...]]) -> str:
    locator = _locator(segments)
    loc_tuple = tuple(segments)
    json_rules = rules_by_locator.get(loc_tuple, ())

    if json_rules:
        matched_rule_locators.add(loc_tuple)
        # Rule explicitly targets this item: it must be a non-archive payload,
        # and it must actually parse as JSON.
        if archive.looks_like_zip(old_bytes) or archive.looks_like_zip(new_bytes):
            unknown.append(
                Unknown(locator, REASON_RULE_TARGET_NOT_JSON,
                        "ignore rule targets a ZIP archive, not JSON")
            )
            return UNDETERMINABLE
        return compare_json_payload(
            old_bytes, new_bytes, json_rules, segments,
            ignored, differences, unknown,
        )

    raw_equal = digest_bytes(old_bytes) == digest_bytes(new_bytes)
    old_is_zip = archive.looks_like_zip(old_bytes)
    new_is_zip = archive.looks_like_zip(new_bytes)

    if old_is_zip != new_is_zip:
        differences.append(
            Difference(
                locator=locator,
                aspect="kind",
                detail="one side is a ZIP archive, the other is not",
                old_digest=digest_bytes(old_bytes),
                new_digest=digest_bytes(new_bytes),
            )
        )
        return DIFFERENT
    if old_is_zip and new_is_zip:
        # Structural validation must run even for byte-identical archives so
        # duplicate members, traversal and corruption are never silently passed.
        status = _compare_zip(
            old_bytes, new_bytes, segments, rules_by_locator, ruleset, budget,
            ignored, differences, unknown, matched_rule_locators,
        )
        return EQUAL if (status == EQUAL and raw_equal) else status

    if raw_equal:
        return EQUAL

    differences.append(
        Difference(
            locator=locator,
            aspect="content",
            detail="file content bytes differ",
            old_digest=digest_bytes(old_bytes),
            new_digest=digest_bytes(new_bytes),
        )
    )
    return DIFFERENT


def _timestamp_ignore_record(member_locator: str,
                             old_ts: archive.Timestamp, new_ts: archive.Timestamp,
                             ignored: list[IgnoreRecord]) -> None:
    old_value = {"date_time": list(old_ts.as_tuple())}
    new_value = {"date_time": list(new_ts.as_tuple())}
    ignored.append(
        IgnoreRecord(
            locator=member_locator,
            aspect="zip-metadata",
            field_path=member_locator + "#zip-metadata/date_time",
            old_digest=digest_bytes(_canonical_dumps(old_value)),
            new_digest=digest_bytes(_canonical_dumps(new_value)),
            old_value=old_value,
            new_value=new_value,
            rule_id=ZIP_TIMESTAMP_RULE_ID,
        )
    )


def _compare_zip(old_bytes: bytes, new_bytes: bytes, segments: list[str],
                 rules_by_locator: dict[tuple[str, ...], tuple[JsonIgnoreRule, ...]],
                 ruleset: RuleSet, budget: archive.Budget,
                 ignored: list[IgnoreRecord], differences: list[Difference],
                 unknown: list[Unknown],
                 matched_rule_locators: set[tuple[str, ...]]) -> str:
    locator = _locator(segments)
    nested_budget = budget.enter()
    try:
        old_entries, _ = archive.read_zip(old_bytes, nested_budget)
        new_entries, _ = archive.read_zip(new_bytes, nested_budget)
    except (CorruptArchiveError, QuotaExceededError) as exc:
        _archive_error_to_unknown(exc, locator, unknown)
        return UNDETERMINABLE

    try:
        return _compare_zip_members(
            old_entries, new_entries, segments, locator, rules_by_locator,
            ruleset, nested_budget, ignored, differences, unknown,
            matched_rule_locators,
        )
    except (CorruptArchiveError, QuotaExceededError) as exc:
        _archive_error_to_unknown(exc, locator, unknown)
        return UNDETERMINABLE


def _compare_zip_members(old_entries, new_entries, segments, locator,
                         rules_by_locator, ruleset, nested_budget,
                         ignored, differences, unknown, matched_rule_locators) -> str:

    old_map = {entry.name: entry for entry in old_entries}
    new_map = {entry.name: entry for entry in new_entries}

    had_unknown = False
    had_difference = False
    had_ignore = False
    all_names = sorted(set(old_map) | set(new_map))
    for name in all_names:
        member_loc = locator + "!/" + name
        old_entry = old_map.get(name)
        new_entry = new_map.get(name)
        if old_entry is None or new_entry is None:
            differences.append(Difference(member_loc, "presence",
                                           "ZIP member exists on only one side"))
            had_difference = True
            continue
        if old_entry.kind != new_entry.kind:
            differences.append(Difference(member_loc, "kind",
                                           f"member kind differs: {old_entry.kind} vs {new_entry.kind}"))
            had_difference = True
            continue

        if _compare_mode(old_entry.mode, new_entry.mode, member_loc, differences):
            had_difference = True

        if old_entry.timestamp.as_tuple() != new_entry.timestamp.as_tuple():
            if ruleset.ignore_zip_timestamps:
                _timestamp_ignore_record(
                    member_loc, old_entry.timestamp, new_entry.timestamp, ignored,
                )
                had_ignore = True
            else:
                differences.append(
                    Difference(
                        member_loc,
                        "zip-metadata",
                        "ZIP member date_time differs and timestamp ignoring is disabled",
                    )
                )
                had_difference = True

        member_loc_tuple = tuple(segments + [name])
        member_has_rule = member_loc_tuple in rules_by_locator
        if old_entry.kind == "symlink":
            if member_has_rule:
                matched_rule_locators.add(member_loc_tuple)
                unknown.append(Unknown(member_loc, REASON_RULE_TARGET_NOT_JSON,
                                       "ignore rule targets a symlink, not JSON"))
                had_unknown = True
            if old_entry.symlink_target != new_entry.symlink_target:
                differences.append(
                    Difference(
                        member_loc, "symlink-target",
                        f"symlink target differs: {old_entry.symlink_target!r} vs {new_entry.symlink_target!r}",
                    )
                )
                had_difference = True
        elif old_entry.kind == "dir":
            if member_has_rule:
                matched_rule_locators.add(member_loc_tuple)
                unknown.append(Unknown(member_loc, REASON_RULE_TARGET_NOT_JSON,
                                       "ignore rule targets a directory, not JSON"))
                had_unknown = True
        elif old_entry.kind == "file":
            if member_has_rule:
                matched_rule_locators.add(member_loc_tuple)
            sub_segments = segments + [name]
            status = _compare_payload(
                old_entry.data, new_entry.data, sub_segments,
                rules_by_locator, ruleset, nested_budget,
                ignored, differences, unknown, matched_rule_locators,
            )
            if status == UNDETERMINABLE:
                had_unknown = True
            elif status == DIFFERENT:
                had_difference = True
            elif status == EQUIVALENT:
                had_ignore = True

    if had_unknown:
        return UNDETERMINABLE
    if had_difference:
        return DIFFERENT
    if had_ignore:
        return EQUIVALENT
    return EQUAL


def compare_trees(old_root: str, new_root: str, ruleset: RuleSet,
                  budget: archive.Budget | None = None) -> ComparisonResult:
    budget = budget or archive.Budget()
    rules_by_locator = ruleset.by_locator()
    matched_rule_locators: set[tuple[str, ...]] = set()

    ignored: list[IgnoreRecord] = []
    differences: list[Difference] = []
    unknown: list[Unknown] = []

    try:
        old_entries = scan_tree(old_root)
        new_entries = scan_tree(new_root)
    except OSError as exc:
        result = ComparisonResult(
            status=UNDETERMINABLE,
            old_root=old_root,
            new_root=new_root,
            old_manifest_digest="",
            new_manifest_digest="",
            unknown=[Unknown(".", REASON_TREE_READ, str(exc))],
        )
        return result

    old_manifest = _manifest_digest(old_entries)
    new_manifest = _manifest_digest(new_entries)

    had_unknown = False
    had_difference = False
    had_ignore = False

    all_names = sorted(set(old_entries) | set(new_entries))
    for rel in all_names:
        locator = rel
        old_entry = old_entries.get(rel)
        new_entry = new_entries.get(rel)

        if old_entry is None or new_entry is None:
            differences.append(Difference(locator, "presence",
                                           "path exists on only one side"))
            had_difference = True
            continue

        if old_entry.kind != new_entry.kind:
            differences.append(Difference(locator, "kind",
                                           f"filesystem kind differs: {old_entry.kind} vs {new_entry.kind}"))
            had_difference = True
            continue

        loc_tuple = (rel,)
        if _compare_mode(old_entry.mode, new_entry.mode, locator, differences):
            had_difference = True

        if loc_tuple in rules_by_locator and old_entry.kind in ("symlink", "dir"):
            matched_rule_locators.add(loc_tuple)
            unknown.append(Unknown(locator, REASON_RULE_TARGET_NOT_JSON,
                                   f"ignore rule targets a {old_entry.kind}, not JSON"))
            had_unknown = True

        if old_entry.kind == "symlink":
            if old_entry.symlink_target != new_entry.symlink_target:
                differences.append(
                    Difference(
                        locator,
                        "symlink-target",
                        f"symlink target differs: {old_entry.symlink_target!r} vs {new_entry.symlink_target!r}",
                    )
                )
                had_difference = True
            continue

        if old_entry.kind == "dir":
            continue

        segments = [rel]

        if loc_tuple in rules_by_locator:
            matched_rule_locators.add(loc_tuple)
            if archive.looks_like_zip(old_entry.data) or archive.looks_like_zip(new_entry.data):
                unknown.append(Unknown(locator, REASON_RULE_TARGET_NOT_JSON,
                                       "ignore rule targets a ZIP archive, not JSON"))
                had_unknown = True
                continue

        status = _compare_payload(
            old_entry.data, new_entry.data, segments,
            rules_by_locator, ruleset, budget,
            ignored, differences, unknown, matched_rule_locators,
        )
        if status == UNDETERMINABLE:
            had_unknown = True
        elif status == DIFFERENT:
            had_difference = True
        elif status == EQUIVALENT:
            had_ignore = True

    # Rules whose target locator matched nothing in any traversed container.
    for loc_tuple in rules_by_locator:
        if loc_tuple not in matched_rule_locators:
            target = "!/".join(loc_tuple)
            unknown.append(Unknown(target, REASON_RULE_TARGET_MISSING,
                                   "no file member matches the rule target"))
            had_unknown = True

    status = _final_status(had_unknown, had_difference, had_ignore)
    unknown = _dedupe_unknown(unknown)
    return ComparisonResult(
        status=status,
        old_root=old_root,
        new_root=new_root,
        old_manifest_digest=old_manifest,
        new_manifest_digest=new_manifest,
        ignored=ignored,
        differences=differences,
        unknown=unknown,
    )


def _dedupe_unknown(items: list[Unknown]) -> list[Unknown]:
    seen: set[tuple[str, str]] = set()
    out: list[Unknown] = []
    for item in items:
        key = (item.locator, item.reason)
        if key in seen:
            continue
        seen.add(key)
        out.append(item)
    return out


def _final_status(had_unknown: bool, had_difference: bool, had_ignore: bool) -> str:
    if had_unknown:
        return UNDETERMINABLE
    if had_difference:
        return DIFFERENT
    if had_ignore:
        return EQUIVALENT
    return EQUAL
