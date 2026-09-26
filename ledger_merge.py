"""Three-way merge for versioned ledger entries.

A ledger entry has a stable string ID, a numeric amount, and a set of tags.
Two branches (left / right) diverge from a common baseline snapshot; this
module merges them with the following semantics:

* Amount (three-way): if exactly one side changed the amount relative to the
  baseline, that side wins; if both sides changed it to the same value, that
  value wins; if both sides changed it to *different* values, an explicit
  conflict is recorded and the merged amount is ``null`` -- never a silent
  "last writer wins".
* Tags (observed-remove set, OR-set style): every tag mutation is an
  operation carrying a unique ``op_id``. An ``add`` op attaches a tag; a
  ``remove`` op lists the ``op_id``s of the add ops it observed and only
  cancels those. Merging unions the operation logs, so a concurrent
  re-add survives a remove that never saw it.
* The merge is deterministic, idempotent and commutative, and conflict
  evidence (baseline value plus both divergent values) is always preserved
  in the output.

Python 3.11+, standard library only. See README.md for the I/O format.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any


class MergeInputError(ValueError):
    """Raised when an input snapshot is malformed."""


# ---------------------------------------------------------------------------
# Validation helpers
# ---------------------------------------------------------------------------

def _check_amount(value: Any, where: str) -> None:
    # bool is a subclass of int; reject it explicitly.
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise MergeInputError(f"{where}: amount must be a number, got {value!r}")


def _validate_tag_op(op: Any, where: str) -> dict:
    if not isinstance(op, dict):
        raise MergeInputError(f"{where}: tag op must be an object, got {op!r}")
    op_id = op.get("op_id")
    kind = op.get("op")
    if not isinstance(op_id, str) or not op_id:
        raise MergeInputError(f"{where}: tag op needs a non-empty string 'op_id'")
    if kind == "add":
        tag = op.get("tag")
        if not isinstance(tag, str) or not tag:
            raise MergeInputError(f"{where}: add op {op_id!r} needs a non-empty string 'tag'")
        return {"op": "add", "op_id": op_id, "tag": tag}
    if kind == "remove":
        removes = op.get("removes")
        if (
            not isinstance(removes, list)
            or not all(isinstance(r, str) for r in removes)
        ):
            raise MergeInputError(
                f"{where}: remove op {op_id!r} needs 'removes' as a list of op_id strings"
            )
        return {"op": "remove", "op_id": op_id, "removes": sorted(set(removes))}
    raise MergeInputError(f"{where}: unknown tag op kind {kind!r} (expected 'add' or 'remove')")


def _validate_entry(entry: Any, where: str) -> dict:
    if not isinstance(entry, dict):
        raise MergeInputError(f"{where}: entry must be an object, got {entry!r}")
    entry_id = entry.get("id")
    if not isinstance(entry_id, str) or not entry_id:
        raise MergeInputError(f"{where}: entry needs a non-empty string 'id'")
    amount = entry.get("amount")
    _check_amount(amount, f"{where}: entry {entry_id!r}")
    raw_ops = entry.get("tag_ops", [])
    if not isinstance(raw_ops, list):
        raise MergeInputError(f"{where}: entry {entry_id!r}: 'tag_ops' must be a list")
    ops = [_validate_tag_op(op, f"{where}: entry {entry_id!r}") for op in raw_ops]
    seen: dict[str, dict] = {}
    for op in ops:
        prior = seen.get(op["op_id"])
        if prior is not None and prior != op:
            raise MergeInputError(
                f"{where}: entry {entry_id!r}: op_id {op['op_id']!r} used by two different ops"
            )
        seen[op["op_id"]] = op
    return {"id": entry_id, "amount": amount, "tag_ops": sorted(seen.values(), key=lambda o: o["op_id"])}


def _validate_snapshot(data: Any, where: str) -> dict[str, dict]:
    if not isinstance(data, dict) or not isinstance(data.get("entries"), list):
        raise MergeInputError(f"{where}: snapshot must be an object with an 'entries' list")
    entries: dict[str, dict] = {}
    for i, raw in enumerate(data["entries"]):
        entry = _validate_entry(raw, f"{where}: entries[{i}]")
        if entry["id"] in entries:
            raise MergeInputError(f"{where}: duplicate entry id {entry['id']!r}")
        entries[entry["id"]] = entry
    return entries


# ---------------------------------------------------------------------------
# Tag set semantics (observed-remove / OR-set)
# ---------------------------------------------------------------------------

def effective_tags(tag_ops: list[dict]) -> list[str]:
    """Compute the live tag set from an operation log.

    A tag is live iff at least one add op for it is not cancelled by any
    remove op. A remove only cancels the add op_ids it explicitly observed.
    """
    adds: dict[str, str] = {}  # op_id -> tag
    removed: set[str] = set()
    for op in tag_ops:
        if op["op"] == "add":
            adds[op["op_id"]] = op["tag"]
        else:
            removed.update(op["removes"])
    live = {tag for op_id, tag in adds.items() if op_id not in removed}
    return sorted(live)


def _merge_tag_ops(
    entry_id: str, left_ops: list[dict], right_ops: list[dict]
) -> tuple[list[dict], list[dict]]:
    """Union two op logs by op_id; conflicting payloads for one op_id are conflicts."""
    merged: dict[str, dict] = {}
    conflicts: list[dict] = []
    for side, ops in (("left", left_ops), ("right", right_ops)):
        for op in ops:
            existing = merged.get(op["op_id"])
            if existing is None:
                merged[op["op_id"]] = op
            elif existing != op:
                conflicts.append(
                    {
                        "entry_id": entry_id,
                        "field": "tag_ops",
                        "op_id": op["op_id"],
                        "reason": "same op_id with different payloads",
                        "values": sorted(
                            [json.dumps(existing, sort_keys=True), json.dumps(op, sort_keys=True)]
                        ),
                    }
                )
    return sorted(merged.values(), key=lambda o: o["op_id"]), conflicts


# ---------------------------------------------------------------------------
# Amount semantics (three-way)
# ---------------------------------------------------------------------------

def _merge_amount(entry_id: str, base: Any, left: Any, right: Any) -> tuple[Any, list[dict]]:
    if left == right:
        return left, []
    if left == base:
        return right, []
    if right == base:
        return left, []
    conflict = {
        "entry_id": entry_id,
        "field": "amount",
        "reason": "both sides changed amount to different values",
        "base": base,
        "values": sorted({left, right}),
    }
    return None, [conflict]


# ---------------------------------------------------------------------------
# Entry / snapshot merge
# ---------------------------------------------------------------------------

def merge_entry(base: dict, left: dict, right: dict) -> tuple[dict, list[dict]]:
    """Merge one entry; returns (merged_entry, conflicts)."""
    entry_id = base["id"]
    amount, amount_conflicts = _merge_amount(entry_id, base["amount"], left["amount"], right["amount"])
    tag_ops, tag_conflicts = _merge_tag_ops(entry_id, left["tag_ops"], right["tag_ops"])
    merged = {
        "id": entry_id,
        "amount": amount,
        "tags": effective_tags(tag_ops),
        "tag_ops": tag_ops,
    }
    return merged, amount_conflicts + tag_conflicts


def merge_snapshots(base: dict, left: dict, right: dict) -> dict:
    """Three-way merge of validated snapshots (id -> entry dicts)."""
    for name, snap in (("left", left), ("right", right)):
        if set(snap) != set(base):
            missing = sorted(set(base) - set(snap))
            extra = sorted(set(snap) - set(base))
            raise MergeInputError(
                f"entry id sets differ: {name} missing {missing}, extra {extra}; "
                "all three snapshots must contain the same entry ids"
            )
    merged_entries = []
    conflicts = []
    for entry_id in sorted(base):
        merged, entry_conflicts = merge_entry(base[entry_id], left[entry_id], right[entry_id])
        merged_entries.append(merged)
        conflicts.extend(entry_conflicts)
    return {
        "status": "conflict" if conflicts else "clean",
        "merged": merged_entries,
        "conflicts": conflicts,
    }


def merge_documents(base_doc: Any, left_doc: Any, right_doc: Any) -> dict:
    """Validate raw JSON documents and merge them."""
    base = _validate_snapshot(base_doc, "base")
    left = _validate_snapshot(left_doc, "left")
    right = _validate_snapshot(right_doc, "right")
    return merge_snapshots(base, left, right)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _load_json(path: str) -> Any:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="ledger-merge",
        description="Three-way merge of ledger snapshots (base + left + right).",
    )
    parser.add_argument("base", help="path to the baseline snapshot JSON")
    parser.add_argument("left", help="path to the left branch snapshot JSON")
    parser.add_argument("right", help="path to the right branch snapshot JSON")
    parser.add_argument(
        "-o", "--output",
        help="write result JSON here instead of stdout",
    )
    parser.add_argument(
        "--compact", action="store_true",
        help="emit compact JSON instead of indented",
    )
    args = parser.parse_args(argv)

    try:
        base_doc = _load_json(args.base)
        left_doc = _load_json(args.left)
        right_doc = _load_json(args.right)
    except OSError as exc:
        print(f"error: cannot read input: {exc}", file=sys.stderr)
        return 2
    except json.JSONDecodeError as exc:
        print(f"error: invalid JSON: {exc}", file=sys.stderr)
        return 2

    try:
        result = merge_documents(base_doc, left_doc, right_doc)
    except MergeInputError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    indent = None if args.compact else 2
    text = json.dumps(result, indent=indent, sort_keys=False, ensure_ascii=False)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)
    return 1 if result["status"] == "conflict" else 0


if __name__ == "__main__":
    raise SystemExit(main())
