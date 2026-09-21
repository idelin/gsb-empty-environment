"""Main comparator for directory trees and nested ZIP archives.

Archives are parsed with the stdlib ``zipfile`` module but never written to
disk.  Recursion is bounded by depth, decompressed-byte and entry-count
limits.  Duplicate entry names, path traversal and corrupt archives make the
affected location undetermined instead of silently picking one entry.
"""

from __future__ import annotations

import hashlib
import os
import stat
import struct
import zipfile
from dataclasses import dataclass
from io import BytesIO

from .jsonutil import JsonError, json_diffs, strict_loads
from .model import (
    EVIDENCE_FORMAT,
    Difference,
    IgnoredRecord,
    Limits,
    Undetermined,
    Verdict,
    encode_value,
    render_key_path,
)
from .rules import JsonKeyRule, RuleError, RuleSet

_ZIP_MAGICS = (b"PK\x03\x04", b"PK\x05\x06")


class _LocalHeaderError(Exception):
    pass


def _looks_like_zip(data: bytes) -> bool:
    return any(data.startswith(magic) for magic in _ZIP_MAGICS)


def _sha256_bytes(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def _sha256_path(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return "sha256:" + digest.hexdigest()


def _safe_entry_name(name: str) -> bool:
    """Reject absolute paths, drive letters and ``..`` traversal."""
    if not name or "\x00" in name:
        return False
    normalized = name.replace("\\", "/")
    if normalized.startswith("/"):
        return False
    if len(normalized) >= 2 and normalized[1] == ":":
        return False
    return ".." not in normalized.split("/")


def _format_datetime(value) -> str:
    return "%04d-%02d-%02dT%02d:%02d:%02d" % tuple(value)


def _zip_other_meta(info):
    """Parser-located entry metadata that is NOT eligible for any ignore rule."""
    return {
        "create_version": info.create_version,
        "extract_version": info.extract_version,
        "flag_bits": info.flag_bits,
        "create_system": info.create_system,
        "internal_attr": info.internal_attr,
        "extra": info.extra.hex(),
    }


def _local_header_digest(archive: bytes, info) -> str:
    """Digest the raw local header region located via the parser's offset."""
    offset = info.header_offset
    if (
        offset < 0
        or offset + 30 > len(archive)
        or archive[offset : offset + 4] != b"PK\x03\x04"
    ):
        raise _LocalHeaderError(info.filename)
    name_len, extra_len = struct.unpack_from("<HH", archive, offset + 26)
    end = offset + 30 + name_len + extra_len
    if end > len(archive):
        raise _LocalHeaderError(info.filename)
    return _sha256_bytes(archive[offset:end])


@dataclass
class _Node:
    kind: str
    mode: int
    target: object
    digest: object
    abspath: str


def _index_tree(root: str) -> dict:
    index = {}

    def walk(abs_dir: str, rel: str) -> None:
        with os.scandir(abs_dir) as entries:
            found = sorted(entries, key=lambda entry: entry.name)
        for entry in found:
            rel_path = f"{rel}/{entry.name}" if rel else entry.name
            info = entry.stat(follow_symlinks=False)
            mode = stat.S_IMODE(info.st_mode)
            if stat.S_ISLNK(info.st_mode):
                index[rel_path] = _Node("symlink", 0, os.readlink(entry.path), None, entry.path)
            elif stat.S_ISDIR(info.st_mode):
                index[rel_path] = _Node("dir", mode, None, None, entry.path)
                walk(entry.path, rel_path)
            elif stat.S_ISREG(info.st_mode):
                index[rel_path] = _Node(
                    "file", mode, None, _sha256_path(entry.path), entry.path
                )
            else:
                index[rel_path] = _Node("other", mode, None, None, entry.path)

    walk(root, "")
    return index


def _tree_digest(index: dict) -> str:
    digest = hashlib.sha256()
    for path in sorted(index):
        node = index[path]
        payload = node.target if node.kind == "symlink" else (node.digest or "")
        line = f"{path}\0{node.kind}\0{node.mode:o}\0{payload}\n"
        digest.update(line.encode("utf-8", "surrogateescape"))
    return "sha256:" + digest.hexdigest()


@dataclass
class ComparisonResult:
    verdict: Verdict
    differences: list
    ignored: list
    undetermined: list
    unused_rules: list
    left_digest: str
    right_digest: str
    rules: RuleSet
    limits: Limits

    def to_evidence(self) -> dict:
        return {
            "format": EVIDENCE_FORMAT,
            "verdict": self.verdict.value,
            "limits": self.limits.to_dict(),
            "inputs": {"left": self.left_digest, "right": self.right_digest},
            "rules": self.rules.to_list(),
            "ignored": [record.to_dict() for record in self.ignored],
            "differences": [record.to_dict() for record in self.differences],
            "undetermined": [record.to_dict() for record in self.undetermined],
            "unused_rules": list(self.unused_rules),
        }


def _covers(key_path: tuple, diff_path: tuple) -> bool:
    return len(key_path) <= len(diff_path) and diff_path[: len(key_path)] == key_path


def _validate_json_rule_path(rule: JsonKeyRule, doc, path: str) -> None:
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


class Comparator:
    """Walks two trees, records differences/ignores/undetermined items."""

    def __init__(self, rules=None, limits=None) -> None:
        self.rules = rules if rules is not None else RuleSet()
        self.limits = limits if limits is not None else Limits()
        self.differences = []
        self.ignored = []
        self.undetermined = []
        self._used_rules = set()
        self._bytes_left = self.limits.max_bytes
        self._entries_left = self.limits.max_entries

    def compare_trees(self, left_root, right_root) -> ComparisonResult:
        left_root = os.fspath(left_root)
        right_root = os.fspath(right_root)
        left_index = _index_tree(left_root)
        right_index = _index_tree(right_root)
        self._validate_rule_targets(left_index, right_index)
        for path in sorted(set(left_index) | set(right_index)):
            left_node = left_index.get(path)
            right_node = right_index.get(path)
            if left_node is None:
                self._diff(path, "path-only-in-right", {"kind": right_node.kind})
                continue
            if right_node is None:
                self._diff(path, "path-only-in-left", {"kind": left_node.kind})
                continue
            self._compare_nodes(path, left_node, right_node)
        return ComparisonResult(
            verdict=self._verdict(),
            differences=self.differences,
            ignored=self.ignored,
            undetermined=self.undetermined,
            unused_rules=sorted(
                rule.rule_id
                for rule in self.rules
                if rule.rule_id not in self._used_rules
            ),
            left_digest=_tree_digest(left_index),
            right_digest=_tree_digest(right_index),
            rules=self.rules,
            limits=self.limits,
        )

    def _validate_rule_targets(self, left_index, right_index) -> None:
        for rule in self.rules:
            if "!" in rule.path:
                continue
            for index in (left_index, right_index):
                node = index.get(rule.path)
                if node is not None and node.kind != "file":
                    raise RuleError(
                        f"rule {rule.rule_id!r} targets {node.kind} at "
                        f"{rule.path!r}; expected a regular file"
                    )

    def _compare_nodes(self, path, left_node, right_node) -> None:
        if left_node.kind != right_node.kind:
            self._diff(
                path,
                "type-changed",
                {"left": left_node.kind, "right": right_node.kind},
            )
            return
        if left_node.kind == "other":
            self._undetermined(path, "unsupported file type")
            return
        if left_node.kind in ("file", "dir") and left_node.mode != right_node.mode:
            self._diff(
                path,
                "mode-changed",
                {"left": oct(left_node.mode), "right": oct(right_node.mode)},
            )
        if left_node.kind == "symlink":
            if left_node.target != right_node.target:
                self._diff(
                    path,
                    "symlink-target-changed",
                    {"left": left_node.target, "right": right_node.target},
                )
        elif left_node.kind == "file" and left_node.digest != right_node.digest:
            with open(left_node.abspath, "rb") as handle:
                left_bytes = handle.read()
            with open(right_node.abspath, "rb") as handle:
                right_bytes = handle.read()
            self._compare_blob_pair(path, left_bytes, right_bytes, depth=1)

    def _compare_blob_pair(self, path, left_bytes, right_bytes, depth) -> None:
        json_rules = self.rules.json_rules_for(path)
        zip_rule = self.rules.zip_rule_for(path)
        if json_rules and zip_rule:
            raise RuleError(f"path {path!r} has both JSON and ZIP rules")
        left_zip = _looks_like_zip(left_bytes)
        right_zip = _looks_like_zip(right_bytes)
        digests = {
            "left_sha256": _sha256_bytes(left_bytes),
            "right_sha256": _sha256_bytes(right_bytes),
        }
        if left_zip and right_zip:
            if json_rules:
                ids = [rule.rule_id for rule in json_rules]
                raise RuleError(f"JSON rule {ids!r} targets ZIP content at {path!r}")
            self._compare_zip(path, left_bytes, right_bytes, depth, zip_rule)
        elif left_zip != right_zip:
            self._diff(path, "file-content-changed", digests)
        else:
            if zip_rule is not None:
                raise RuleError(
                    f"ZIP timestamp rule {zip_rule.rule_id!r} targets non-ZIP "
                    f"content at {path!r}"
                )
            if json_rules:
                self._compare_json(path, left_bytes, right_bytes, json_rules)
            else:
                self._compare_sniffed_content(
                    path, left_bytes, right_bytes, digests
                )

    def _compare_sniffed_content(self, path, left_bytes, right_bytes, digests) -> None:
        """Without an explicit JSON rule, sniff both blobs; compare as JSON
        only when both sides parse.  Everything else stays a byte diff."""
        try:
            left_doc = strict_loads(left_bytes)
            right_doc = strict_loads(right_bytes)
        except JsonError:
            self._diff(path, "file-content-changed", digests)
            return
        self._json_docs(
            path, left_bytes, right_bytes, left_doc, right_doc, ()
        )

    def _compare_zip(self, path, left_bytes, right_bytes, depth, zip_rule) -> None:
        if depth > self.limits.max_depth:
            self._undetermined(
                path,
                f"archive nesting depth {depth} exceeds limit "
                f"{self.limits.max_depth}",
            )
            return
        try:
            left_zip = zipfile.ZipFile(BytesIO(left_bytes))
            right_zip = zipfile.ZipFile(BytesIO(right_bytes))
        except zipfile.BadZipFile as exc:
            self._undetermined(path, f"corrupt ZIP archive: {exc}")
            return
        with left_zip, right_zip:
            left_infos = left_zip.infolist()
            right_infos = right_zip.infolist()
            for infos, side in ((left_infos, "left"), (right_infos, "right")):
                seen = set()
                for info in infos:
                    if info.filename in seen:
                        self._undetermined(
                            path,
                            f"duplicate entry name {info.filename!r} in {side} "
                            f"archive",
                        )
                        return
                    seen.add(info.filename)
                    if not _safe_entry_name(info.filename):
                        self._undetermined(
                            path,
                            f"unsafe entry path {info.filename!r} in {side} "
                            f"archive",
                        )
                        return
            self._entries_left -= len(left_infos) + len(right_infos)
            if self._entries_left < 0:
                self._undetermined(
                    path,
                    f"entry count limit {self.limits.max_entries} exceeded",
                )
                return
            if left_zip.comment != right_zip.comment:
                self._diff(path, "zip-comment-changed", {})
            left_names = [info.filename for info in left_infos]
            right_names = [info.filename for info in right_infos]
            left_set, right_set = set(left_names), set(right_names)
            for name in sorted(left_set - right_set):
                self._diff(path, "zip-entry-removed", {"entry": name})
            for name in sorted(right_set - left_set):
                self._diff(path, "zip-entry-added", {"entry": name})
            if left_set == right_set and left_names != right_names:
                self._diff(
                    path,
                    "zip-entry-order-changed",
                    {"left": left_names, "right": right_names},
                )
            right_map = {info.filename: info for info in right_infos}
            for info in left_infos:
                right_info = right_map.get(info.filename)
                if right_info is not None:
                    self._compare_zip_entry(
                        path,
                        left_bytes,
                        right_bytes,
                        left_zip,
                        right_zip,
                        info,
                        right_info,
                        depth,
                        zip_rule,
                    )

    def _compare_zip_entry(
        self,
        path,
        left_archive,
        right_archive,
        left_zip,
        right_zip,
        left_info,
        right_info,
        depth,
        zip_rule,
    ) -> None:
        name = left_info.filename
        entry_path = f"{path}!{name}"
        if left_info.compress_type != right_info.compress_type:
            self._diff(
                entry_path,
                "zip-entry-compression-changed",
                {"left": left_info.compress_type, "right": right_info.compress_type},
            )
        left_mode = (left_info.external_attr >> 16) & 0o7777
        right_mode = (right_info.external_attr >> 16) & 0o7777
        if left_mode != right_mode:
            self._diff(
                entry_path,
                "zip-entry-mode-changed",
                {"left": oct(left_mode), "right": oct(right_mode)},
            )
        left_meta = _zip_other_meta(left_info)
        right_meta = _zip_other_meta(right_info)
        for field in left_meta:
            if left_meta[field] != right_meta[field]:
                self._diff(
                    entry_path,
                    "zip-entry-metadata-changed",
                    {
                        "field": field,
                        "left": left_meta[field],
                        "right": right_meta[field],
                    },
                )
        if left_info.date_time != right_info.date_time:
            if zip_rule is not None:
                try:
                    old_digest = _local_header_digest(left_archive, left_info)
                    new_digest = _local_header_digest(right_archive, right_info)
                except _LocalHeaderError:
                    self._undetermined(
                        entry_path, "local header not found at recorded offset"
                    )
                    return
                self._used_rules.add(zip_rule.rule_id)
                self.ignored.append(
                    IgnoredRecord(
                        rule_id=zip_rule.rule_id,
                        path=path,
                        field_path=f"{name}#date_time",
                        kind="zip-entry-metadata-changed",
                        old=_format_datetime(left_info.date_time),
                        new=_format_datetime(right_info.date_time),
                        old_digest=old_digest,
                        new_digest=new_digest,
                    )
                )
            else:
                self._diff(
                    entry_path,
                    "zip-entry-metadata-changed",
                    {
                        "field": "date_time",
                        "left": _format_datetime(left_info.date_time),
                        "right": _format_datetime(right_info.date_time),
                    },
                )
        if name.endswith("/"):
            return
        try:
            left_data = left_zip.read(left_info)
            right_data = right_zip.read(right_info)
        except (zipfile.BadZipFile, NotImplementedError, RuntimeError) as exc:
            self._undetermined(entry_path, f"unreadable entry: {exc}")
            return
        self._bytes_left -= len(left_data) + len(right_data)
        if self._bytes_left < 0:
            self._undetermined(
                path,
                f"decompressed byte limit {self.limits.max_bytes} exceeded",
            )
            return
        if left_data == right_data:
            return
        self._compare_blob_pair(entry_path, left_data, right_data, depth + 1)

    def _compare_json(self, path, left_bytes, right_bytes, json_rules) -> None:
        try:
            left_doc = strict_loads(left_bytes)
        except JsonError as exc:
            self._undetermined(path, f"invalid JSON in left input: {exc}")
            return
        try:
            right_doc = strict_loads(right_bytes)
        except JsonError as exc:
            self._undetermined(path, f"invalid JSON in right input: {exc}")
            return
        for rule in json_rules:
            _validate_json_rule_path(rule, left_doc, path)
            _validate_json_rule_path(rule, right_doc, path)
        self._json_docs(
            path, left_bytes, right_bytes, left_doc, right_doc, json_rules
        )

    def _json_docs(self, path, left_bytes, right_bytes,
                   left_doc, right_doc, json_rules) -> None:
        old_digest = _sha256_bytes(left_bytes)
        new_digest = _sha256_bytes(right_bytes)
        leaf_diffs = list(json_diffs(left_doc, right_doc))
        if not leaf_diffs:
            self._diff(
                path,
                "json-formatting-changed",
                {"left_sha256": old_digest, "right_sha256": new_digest},
            )
            return
        for diff in leaf_diffs:
            rule = next(
                (
                    candidate
                    for candidate in json_rules
                    if _covers(candidate.key_path, diff.key_path)
                ),
                None,
            )
            field_path = render_key_path(diff.key_path)
            if rule is not None:
                self._used_rules.add(rule.rule_id)
                self.ignored.append(
                    IgnoredRecord(
                        rule_id=rule.rule_id,
                        path=path,
                        field_path=field_path,
                        kind=diff.kind,
                        old=encode_value(diff.old),
                        new=encode_value(diff.new),
                        old_digest=old_digest,
                        new_digest=new_digest,
                    )
                )
            else:
                self._diff(
                    path,
                    diff.kind,
                    {
                        "key_path": field_path,
                        "left": encode_value(diff.old),
                        "right": encode_value(diff.new),
                    },
                )

    def _diff(self, path, kind, detail) -> None:
        self.differences.append(Difference(path=path, kind=kind, detail=detail))

    def _undetermined(self, path, reason) -> None:
        self.undetermined.append(Undetermined(path=path, reason=reason))

    def _verdict(self) -> Verdict:
        if self.differences:
            return Verdict.DIFFERENT
        if self.undetermined:
            return Verdict.UNDETERMINED
        if self.ignored:
            return Verdict.EQUIVALENT
        return Verdict.IDENTICAL


def compare_trees(left_root, right_root, rules=None, limits=None) -> ComparisonResult:
    return Comparator(rules=rules, limits=limits).compare_trees(left_root, right_root)
