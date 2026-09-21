"""Independent evidence verifier.

Re-confirms a comparison conclusion using only the two raw input trees,
the rule set and the evidence package.  Nothing produced by the main
comparator is trusted: the tree walk, archive parsing, JSON leaf diffs and
raw-region digests are all re-derived here from the raw inputs.
"""

from __future__ import annotations

import hashlib
import json
import os
import stat
import struct
import zipfile
from collections import Counter
from dataclasses import dataclass
from io import BytesIO

from .jsonutil import JsonError, strict_loads
from .model import EVIDENCE_FORMAT, MISSING, Limits, encode_value, render_key_path
from .rules import RuleError, RuleSet

_MAGICS = (b"PK\x03\x04", b"PK\x05\x06")


@dataclass
class VerificationReport:
    ok: bool
    verdict: object
    problems: list


def _digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def _looks_zipped(data: bytes) -> bool:
    return any(data.startswith(magic) for magic in _MAGICS)


def _safe_name(name: str) -> bool:
    if not name or "\x00" in name:
        return False
    normalized = name.replace("\\", "/")
    if normalized.startswith("/"):
        return False
    if len(normalized) >= 2 and normalized[1] == ":":
        return False
    return ".." not in normalized.split("/")


def _dt(value) -> str:
    return "%04d-%02d-%02dT%02d:%02d:%02d" % tuple(value)


def _other_meta(info) -> dict:
    return {
        "create_version": info.create_version,
        "extract_version": info.extract_version,
        "flag_bits": info.flag_bits,
        "create_system": info.create_system,
        "internal_attr": info.internal_attr,
        "extra": info.extra.hex(),
    }


class _HeaderLocator(Exception):
    pass


def _header_digest(archive: bytes, info) -> str:
    offset = info.header_offset
    if (
        offset < 0
        or offset + 30 > len(archive)
        or archive[offset : offset + 4] != b"PK\x03\x04"
    ):
        raise _HeaderLocator(info.filename)
    name_len, extra_len = struct.unpack_from("<HH", archive, offset + 26)
    end = offset + 30 + name_len + extra_len
    if end > len(archive):
        raise _HeaderLocator(info.filename)
    return _digest(archive[offset:end])


def _is_number(value) -> bool:
    import decimal

    return isinstance(value, (int, decimal.Decimal)) and not isinstance(value, bool)


def _json_leaf_diffs(old, new, path=()):
    """Independent copy of the JSON leaf-diff algorithm."""
    if _is_number(old) and _is_number(new):
        if old != new:
            yield path, "value-changed", old, new
        return
    if type(old) is not type(new):
        yield path, "type-changed", old, new
        return
    if isinstance(old, dict):
        for key in old:
            if key not in new:
                yield path + (key,), "key-removed", old[key], MISSING
        for key in new:
            if key not in old:
                yield path + (key,), "key-added", MISSING, new[key]
        for key in old:
            if key in new:
                yield from _json_leaf_diffs(old[key], new[key], path + (key,))
    elif isinstance(old, list):
        common = min(len(old), len(new))
        for index in range(common):
            yield from _json_leaf_diffs(old[index], new[index], path + (index,))
        for index in range(common, len(old)):
            yield path + (index,), "element-removed", old[index], MISSING
        for index in range(common, len(new)):
            yield path + (index,), "element-added", MISSING, new[index]
    elif old != new:
        yield path, "value-changed", old, new


def _covered(key_path, diff_path) -> bool:
    return len(key_path) <= len(diff_path) and diff_path[: len(key_path)] == key_path


def _check_rule_path(rule, doc, path) -> None:
    node = doc
    for elem in rule.key_path:
        if isinstance(elem, str):
            if not isinstance(node, dict):
                raise RuleError(
                    f"rule {rule.rule_id!r}: key {elem!r} traverses a non-object "
                    f"value in {path!r}"
                )
            if elem not in node:
                return
            node = node[elem]
        else:
            if not isinstance(node, list):
                raise RuleError(
                    f"rule {rule.rule_id!r}: index [{elem}] traverses a non-array "
                    f"value in {path!r}"
                )
            if elem >= len(node):
                return
            node = node[elem]


class _Review:
    """Self-contained re-implementation of the comparison walk."""

    def __init__(self, rules: RuleSet, limits: Limits) -> None:
        self.rules = rules
        self.limits = limits
        self.differences = []
        self.ignored = []
        self.undetermined = []
        self.used = set()
        self._bytes_left = limits.max_bytes
        self._entries_left = limits.max_entries

    def run(self, left_root: str, right_root: str) -> "_Review":
        left_index = self._scan(left_root)
        right_index = self._scan(right_root)
        self.left_digest = self._tree_digest(left_index)
        self.right_digest = self._tree_digest(right_index)
        for rule in self.rules:
            if "!" in rule.path:
                continue
            for index in (left_index, right_index):
                node = index.get(rule.path)
                if node is not None and node[0] != "file":
                    raise RuleError(
                        f"rule {rule.rule_id!r} targets {node[0]} at "
                        f"{rule.path!r}; expected a regular file"
                    )
        for path in sorted(set(left_index) | set(right_index)):
            left_node = left_index.get(path)
            right_node = right_index.get(path)
            if left_node is None:
                self.differences.append(
                    {"path": path, "kind": "path-only-in-right",
                     "detail": {"kind": right_node[0]}}
                )
                continue
            if right_node is None:
                self.differences.append(
                    {"path": path, "kind": "path-only-in-left",
                     "detail": {"kind": left_node[0]}}
                )
                continue
            self._nodes(path, left_node, right_node)
        return self

    def _scan(self, root: str) -> dict:
        found_all = {}

        def walk(abs_dir, rel):
            with os.scandir(abs_dir) as entries:
                entries_sorted = sorted(entries, key=lambda item: item.name)
            for entry in entries_sorted:
                rel_path = f"{rel}/{entry.name}" if rel else entry.name
                info = entry.stat(follow_symlinks=False)
                mode = stat.S_IMODE(info.st_mode)
                if stat.S_ISLNK(info.st_mode):
                    found_all[rel_path] = (
                        "symlink", 0, os.readlink(entry.path), entry.path
                    )
                elif stat.S_ISDIR(info.st_mode):
                    found_all[rel_path] = ("dir", mode, None, entry.path)
                    walk(entry.path, rel_path)
                elif stat.S_ISREG(info.st_mode):
                    with open(entry.path, "rb") as handle:
                        digest = "sha256:" + hashlib.sha256(handle.read()).hexdigest()
                    found_all[rel_path] = ("file", mode, digest, entry.path)
                else:
                    found_all[rel_path] = ("other", mode, None, entry.path)

        walk(root, "")
        return found_all

    def _tree_digest(self, index: dict) -> str:
        digest = hashlib.sha256()
        for path in sorted(index):
            kind, mode, payload, _ = index[path]
            payload = payload if kind == "symlink" else (payload or "")
            line = f"{path}\0{kind}\0{mode:o}\0{payload}\n"
            digest.update(line.encode("utf-8", "surrogateescape"))
        return "sha256:" + digest.hexdigest()

    def _nodes(self, path, left_node, right_node) -> None:
        left_kind = left_node[0]
        right_kind = right_node[0]
        if left_kind != right_kind:
            self.differences.append(
                {"path": path, "kind": "type-changed",
                 "detail": {"left": left_kind, "right": right_kind}}
            )
            return
        if left_kind == "other":
            self.undetermined.append(
                {"path": path, "reason": "unsupported file type"}
            )
            return
        if left_kind in ("file", "dir") and left_node[1] != right_node[1]:
            self.differences.append(
                {"path": path, "kind": "mode-changed",
                 "detail": {"left": oct(left_node[1]), "right": oct(right_node[1])}}
            )
        if left_kind == "symlink":
            if left_node[2] != right_node[2]:
                self.differences.append(
                    {"path": path, "kind": "symlink-target-changed",
                     "detail": {"left": left_node[2], "right": right_node[2]}}
                )
            return
        if left_kind == "file" and left_node[2] != right_node[2]:
            with open(left_node[3], "rb") as handle:
                left_bytes = handle.read()
            with open(right_node[3], "rb") as handle:
                right_bytes = handle.read()
            self._blob(path, left_bytes, right_bytes, 1)

    def _blob(self, path, left_bytes, right_bytes, depth) -> None:
        json_rules = self.rules.json_rules_for(path)
        zip_rule = self.rules.zip_rule_for(path)
        if json_rules and zip_rule:
            raise RuleError(f"path {path!r} has both JSON and ZIP rules")
        left_zip = _looks_zipped(left_bytes)
        right_zip = _looks_zipped(right_bytes)
        digests = {"left_sha256": _digest(left_bytes),
                   "right_sha256": _digest(right_bytes)}
        if left_zip and right_zip:
            if json_rules:
                ids = [rule.rule_id for rule in json_rules]
                raise RuleError(f"JSON rule {ids!r} targets ZIP content at {path!r}")
            self._zip(path, left_bytes, right_bytes, depth, zip_rule)
        elif left_zip != right_zip:
            self.differences.append(
                {"path": path, "kind": "file-content-changed", "detail": digests}
            )
        else:
            if zip_rule is not None:
                raise RuleError(
                    f"ZIP timestamp rule {zip_rule.rule_id!r} targets non-ZIP "
                    f"content at {path!r}"
                )
            if json_rules:
                self._json(path, left_bytes, right_bytes, json_rules)
            else:
                try:
                    left_doc = strict_loads(left_bytes)
                    right_doc = strict_loads(right_bytes)
                except JsonError:
                    self.differences.append(
                        {"path": path, "kind": "file-content-changed",
                         "detail": digests}
                    )
                else:
                    self._json_docs(
                        path, left_bytes, right_bytes,
                        left_doc, right_doc, (),
                    )

    def _zip(self, path, left_bytes, right_bytes, depth, zip_rule) -> None:
        if depth > self.limits.max_depth:
            self.undetermined.append(
                {"path": path,
                 "reason": f"archive nesting depth {depth} exceeds limit "
                           f"{self.limits.max_depth}"}
            )
            return
        try:
            left_zip = zipfile.ZipFile(BytesIO(left_bytes))
            right_zip = zipfile.ZipFile(BytesIO(right_bytes))
        except zipfile.BadZipFile as exc:
            self.undetermined.append(
                {"path": path, "reason": f"corrupt ZIP archive: {exc}"}
            )
            return
        with left_zip, right_zip:
            left_infos = left_zip.infolist()
            right_infos = right_zip.infolist()
            for infos, side in ((left_infos, "left"), (right_infos, "right")):
                seen = set()
                for info in infos:
                    if info.filename in seen:
                        self.undetermined.append(
                            {"path": path,
                             "reason": f"duplicate entry name {info.filename!r} "
                                       f"in {side} archive"}
                        )
                        return
                    seen.add(info.filename)
                    if not _safe_name(info.filename):
                        self.undetermined.append(
                            {"path": path,
                             "reason": f"unsafe entry path {info.filename!r} "
                                       f"in {side} archive"}
                        )
                        return
            self._entries_left -= len(left_infos) + len(right_infos)
            if self._entries_left < 0:
                self.undetermined.append(
                    {"path": path,
                     "reason": f"entry count limit {self.limits.max_entries} "
                               f"exceeded"}
                )
                return
            if left_zip.comment != right_zip.comment:
                self.differences.append(
                    {"path": path, "kind": "zip-comment-changed", "detail": {}}
                )
            left_names = [info.filename for info in left_infos]
            right_names = [info.filename for info in right_infos]
            left_set, right_set = set(left_names), set(right_names)
            for name in sorted(left_set - right_set):
                self.differences.append(
                    {"path": path, "kind": "zip-entry-removed",
                     "detail": {"entry": name}}
                )
            for name in sorted(right_set - left_set):
                self.differences.append(
                    {"path": path, "kind": "zip-entry-added",
                     "detail": {"entry": name}}
                )
            if left_set == right_set and left_names != right_names:
                self.differences.append(
                    {"path": path, "kind": "zip-entry-order-changed",
                     "detail": {"left": left_names, "right": right_names}}
                )
            right_map = {info.filename: info for info in right_infos}
            for info in left_infos:
                other = right_map.get(info.filename)
                if other is not None:
                    self._zip_entry(
                        path, left_bytes, right_bytes,
                        left_zip, right_zip, info, other, depth, zip_rule,
                    )

    def _zip_entry(
        self, path, left_archive, right_archive,
        left_zip, right_zip, left_info, right_info, depth, zip_rule,
    ) -> None:
        name = left_info.filename
        entry_path = f"{path}!{name}"
        if left_info.compress_type != right_info.compress_type:
            self.differences.append(
                {"path": entry_path, "kind": "zip-entry-compression-changed",
                 "detail": {"left": left_info.compress_type,
                            "right": right_info.compress_type}}
            )
        left_mode = (left_info.external_attr >> 16) & 0o7777
        right_mode = (right_info.external_attr >> 16) & 0o7777
        if left_mode != right_mode:
            self.differences.append(
                {"path": entry_path, "kind": "zip-entry-mode-changed",
                 "detail": {"left": oct(left_mode), "right": oct(right_mode)}}
            )
        left_meta = _other_meta(left_info)
        right_meta = _other_meta(right_info)
        for field in left_meta:
            if left_meta[field] != right_meta[field]:
                self.differences.append(
                    {"path": entry_path, "kind": "zip-entry-metadata-changed",
                     "detail": {"field": field,
                                "left": left_meta[field],
                                "right": right_meta[field]}}
                )
        if left_info.date_time != right_info.date_time:
            if zip_rule is not None:
                try:
                    old_digest = _header_digest(left_archive, left_info)
                    new_digest = _header_digest(right_archive, right_info)
                except _HeaderLocator:
                    self.undetermined.append(
                        {"path": entry_path,
                         "reason": "local header not found at recorded offset"}
                    )
                    return
                self.used.add(zip_rule.rule_id)
                self.ignored.append(
                    {
                        "rule_id": zip_rule.rule_id,
                        "path": path,
                        "field_path": f"{name}#date_time",
                        "kind": "zip-entry-metadata-changed",
                        "old": _dt(left_info.date_time),
                        "new": _dt(right_info.date_time),
                        "old_digest": old_digest,
                        "new_digest": new_digest,
                    }
                )
            else:
                self.differences.append(
                    {"path": entry_path, "kind": "zip-entry-metadata-changed",
                     "detail": {"field": "date_time",
                                "left": _dt(left_info.date_time),
                                "right": _dt(right_info.date_time)}}
                )
        if name.endswith("/"):
            return
        try:
            left_data = left_zip.read(left_info)
            right_data = right_zip.read(right_info)
        except (zipfile.BadZipFile, NotImplementedError, RuntimeError) as exc:
            self.undetermined.append(
                {"path": entry_path, "reason": f"unreadable entry: {exc}"}
            )
            return
        self._bytes_left -= len(left_data) + len(right_data)
        if self._bytes_left < 0:
            self.undetermined.append(
                {"path": path,
                 "reason": f"decompressed byte limit {self.limits.max_bytes} "
                           f"exceeded"}
            )
            return
        if left_data != right_data:
            self._blob(entry_path, left_data, right_data, depth + 1)

    def _json(self, path, left_bytes, right_bytes, json_rules) -> None:
        try:
            left_doc = strict_loads(left_bytes)
        except JsonError as exc:
            self.undetermined.append(
                {"path": path, "reason": f"invalid JSON in left input: {exc}"}
            )
            return
        try:
            right_doc = strict_loads(right_bytes)
        except JsonError as exc:
            self.undetermined.append(
                {"path": path, "reason": f"invalid JSON in right input: {exc}"}
            )
            return
        for rule in json_rules:
            _check_rule_path(rule, left_doc, path)
            _check_rule_path(rule, right_doc, path)
        self._json_docs(
            path, left_bytes, right_bytes, left_doc, right_doc, json_rules
        )

    def _json_docs(self, path, left_bytes, right_bytes,
                   left_doc, right_doc, json_rules) -> None:
        old_digest = _digest(left_bytes)
        new_digest = _digest(right_bytes)
        leaf_diffs = list(_json_leaf_diffs(left_doc, right_doc))
        if not leaf_diffs:
            self.differences.append(
                {"path": path, "kind": "json-formatting-changed",
                 "detail": {"left_sha256": old_digest,
                            "right_sha256": new_digest}}
            )
            return
        for key_path, kind, old, new in leaf_diffs:
            rule = next(
                (
                    candidate
                    for candidate in json_rules
                    if _covered(candidate.key_path, key_path)
                ),
                None,
            )
            field_path = render_key_path(key_path)
            if rule is not None:
                self.used.add(rule.rule_id)
                self.ignored.append(
                    {
                        "rule_id": rule.rule_id,
                        "path": path,
                        "field_path": field_path,
                        "kind": kind,
                        "old": encode_value(old),
                        "new": encode_value(new),
                        "old_digest": old_digest,
                        "new_digest": new_digest,
                    }
                )
            else:
                self.differences.append(
                    {
                        "path": path,
                        "kind": kind,
                        "detail": {
                            "key_path": field_path,
                            "left": encode_value(old),
                            "right": encode_value(new),
                        },
                    }
                )

    def verdict(self) -> str:
        if self.differences:
            return "different"
        if self.undetermined:
            return "undetermined"
        if self.ignored:
            return "equivalent"
        return "identical"

    def unused_rules(self) -> list:
        return sorted(
            rule.rule_id for rule in self.rules if rule.rule_id not in self.used
        )


def _canonical(items) -> Counter:
    return Counter(json.dumps(item, sort_keys=True) for item in items)


def _check_section(problems, name, claimed, recomputed) -> None:
    claimed = claimed if isinstance(claimed, list) else []
    claimed_counter = _canonical(claimed)
    recomputed_counter = _canonical(recomputed)
    if claimed_counter == recomputed_counter:
        return
    missing = list((recomputed_counter - claimed_counter).elements())
    extra = list((claimed_counter - recomputed_counter).elements())
    detail = []
    if missing:
        detail.append(f"missing from evidence: {missing[:3]}")
    if extra:
        detail.append(f"not reproducible from raw inputs: {extra[:3]}")
    problems.append(f"{name} section mismatch ({'; '.join(detail)})")


def verify_evidence(left_root, right_root, rules: RuleSet, evidence: dict):
    """Re-confirm an evidence package against the two raw input trees."""
    if not isinstance(evidence, dict) or evidence.get("format") != EVIDENCE_FORMAT:
        return VerificationReport(
            False, None, ["unsupported or missing evidence format"]
        )
    try:
        limits = Limits.from_dict(evidence["limits"])
    except (KeyError, TypeError, ValueError) as exc:
        return VerificationReport(False, None, [f"bad limits in evidence: {exc}"])

    review = _Review(rules, limits).run(os.fspath(left_root), os.fspath(right_root))
    problems = []

    expected_inputs = {"left": review.left_digest, "right": review.right_digest}
    if evidence.get("inputs") != expected_inputs:
        problems.append("input digests do not match the raw inputs")
    if evidence.get("rules") != rules.to_list():
        problems.append("rule set in evidence does not match the provided rules")

    _check_section(problems, "ignored", evidence.get("ignored"), review.ignored)
    _check_section(
        problems, "differences", evidence.get("differences"), review.differences
    )
    _check_section(
        problems, "undetermined", evidence.get("undetermined"), review.undetermined
    )

    claimed_unused = evidence.get("unused_rules")
    if not isinstance(claimed_unused, list) or sorted(claimed_unused) != review.unused_rules():
        problems.append("unused rule list mismatch")

    verdict = review.verdict()
    if evidence.get("verdict") != verdict:
        problems.append(
            f"verdict mismatch: evidence claims {evidence.get('verdict')!r}, "
            f"independent review computes {verdict!r}"
        )
    return VerificationReport(not problems, verdict, problems)
