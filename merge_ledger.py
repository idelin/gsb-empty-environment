#!/usr/bin/env python3
"""分支账本三方合并（three-way merge for branched ledgers）。

账本条目：稳定 ID、金额、标签集合。两个分支从共同基线修改后合并：

- 不同字段的修改可自动合并；
- 同字段（金额）被改为不同值时产生显式冲突，绝不静默选边；
- 标签采用带操作 ID 的增删集合语义（observed-remove set）：
  删除操作只移除它在所属分支中已观察到的添加操作；
- 合并满足幂等、交换律，且不丢失冲突证据。

仅使用 Python 3.11 标准库。命令行用法见 README.md 或 `python3 merge_ledger.py -h`。
"""

from __future__ import annotations

import argparse
import copy
import json
import sys
from typing import Any

BASE_TAG_OP_PREFIX = "base:"


# ---------------------------------------------------------------------------
# 状态构造
# ---------------------------------------------------------------------------

def base_state(entries: list[dict[str, Any]]) -> dict[str, Any]:
    """由基线条目列表构造基线状态。

    每个基线标签被赋予一个确定性的添加操作 ID：``base:<entry_id>:<tag>``，
    使后续分支的删除操作可以精确指向它。
    """
    state: dict[str, Any] = {"entries": {}}
    for entry in entries:
        entry_id = entry["id"]
        if entry_id in state["entries"]:
            raise ValueError(f"duplicate entry id in base: {entry_id!r}")
        adds = {}
        for tag in entry.get("tags", []):
            adds[f"{BASE_TAG_OP_PREFIX}{entry_id}:{tag}"] = tag
        state["entries"][entry_id] = {
            "base_amount": entry["amount"],
            "amount_ops": {},
            "tag_adds": adds,
            "tag_removes": {},
        }
    return state


def _present_add_ids(entry_state: dict[str, Any], tag: str) -> list[str]:
    removed: set[str] = set()
    for rem in entry_state["tag_removes"].values():
        removed.update(rem["removes"])
    return sorted(
        op_id
        for op_id, added_tag in entry_state["tag_adds"].items()
        if added_tag == tag and op_id not in removed
    )


def apply_ops(state: dict[str, Any], ops: list[dict[str, Any]]) -> dict[str, Any]:
    """在状态副本上按顺序应用一个分支的操作日志，返回分支状态。

    支持的操作：
    - ``set_amount``: ``{"op_id", "op": "set_amount", "entry", "amount"}``
      同一分支内后写覆盖先写（单一有序历史，属分支内部语义）。
    - ``add_tag``:    ``{"op_id", "op": "add_tag", "entry", "tag"}``
    - ``remove_tag``: ``{"op_id", "op": "remove_tag", "entry", "tag"[, "removes"]}``
      未显式给出 ``removes`` 时，自动记录该分支当前已观察到的、
      针对该标签仍然生效的添加操作 ID 集合。
    """
    result = copy.deepcopy(state)
    for op in ops:
        kind = op.get("op")
        op_id = op.get("op_id")
        entry_id = op.get("entry")
        if not op_id:
            raise ValueError(f"operation missing op_id: {op!r}")
        if entry_id not in result["entries"]:
            raise ValueError(f"operation targets unknown entry {entry_id!r}: {op!r}")
        entry_state = result["entries"][entry_id]

        if kind == "set_amount":
            # 分支内只保留最新的金额操作。
            entry_state["amount_ops"] = {op_id: op["amount"]}
        elif kind == "add_tag":
            if op_id in entry_state["tag_adds"] or op_id in entry_state["tag_removes"]:
                raise ValueError(f"duplicate op_id {op_id!r}")
            entry_state["tag_adds"][op_id] = op["tag"]
        elif kind == "remove_tag":
            if op_id in entry_state["tag_adds"] or op_id in entry_state["tag_removes"]:
                raise ValueError(f"duplicate op_id {op_id!r}")
            observed = op.get("removes")
            if observed is None:
                observed = _present_add_ids(entry_state, op["tag"])
            entry_state["tag_removes"][op_id] = {
                "tag": op["tag"],
                "removes": sorted(observed),
            }
        else:
            raise ValueError(f"unknown op kind {kind!r}: {op!r}")
    return result


# ---------------------------------------------------------------------------
# 合并
# ---------------------------------------------------------------------------

def _union_ops(
    left: dict[str, Any], right: dict[str, Any], what: str, entry_id: str
) -> dict[str, Any]:
    """按键合并两个字典；同键不同值视为操作 ID 冲突（拒绝静默覆盖证据）。"""
    merged = dict(left)
    for key, value in right.items():
        if key in merged and merged[key] != value:
            raise ValueError(
                f"op_id {key!r} has conflicting {what} on entry {entry_id!r}; "
                "op_ids must be unique across branches"
            )
        merged[key] = value
    return merged


def merge(
    base: dict[str, Any], left: dict[str, Any], right: dict[str, Any]
) -> dict[str, Any]:
    """三方合并：对基线派生的两个状态做集合并，返回合并后的状态。

    金额操作、标签增删操作均按操作 ID 做集合并，因此合并天然满足
    交换律与幂等律；冲突在 ``resolve`` 的派生视图中确定性地呈现。
    """
    entry_ids = set(base["entries"]) | set(left["entries"]) | set(right["entries"])
    merged: dict[str, Any] = {"entries": {}}
    for entry_id in sorted(entry_ids):
        states = [
            s["entries"][entry_id]
            for s in (base, left, right)
            if entry_id in s["entries"]
        ]
        base_amounts = {s["base_amount"] for s in states}
        if len(base_amounts) != 1:
            raise ValueError(
                f"inconsistent base_amount for entry {entry_id!r}: {base_amounts}"
            )
        base_entry = base["entries"].get(entry_id)
        merged["entries"][entry_id] = {
            "base_amount": states[0]["base_amount"],
            "amount_ops": _union_ops(
                left["entries"].get(entry_id, {}).get("amount_ops", {}),
                right["entries"].get(entry_id, {}).get("amount_ops", {}),
                "amounts",
                entry_id,
            ),
            "tag_adds": _union_ops(
                left["entries"].get(entry_id, {}).get("tag_adds", {}),
                right["entries"].get(entry_id, {}).get("tag_adds", {}),
                "tag adds",
                entry_id,
            ),
            "tag_removes": _union_ops(
                left["entries"].get(entry_id, {}).get("tag_removes", {}),
                right["entries"].get(entry_id, {}).get("tag_removes", {}),
                "tag removes",
                entry_id,
            ),
        }
        if base_entry is not None:
            # 基线中的标签添加操作必须保留（删除操作可能引用它们）。
            for op_id, tag in base_entry["tag_adds"].items():
                merged["entries"][entry_id]["tag_adds"].setdefault(op_id, tag)
    return merged


# ---------------------------------------------------------------------------
# 派生视图（含冲突报告）
# ---------------------------------------------------------------------------

def resolve(state: dict[str, Any]) -> dict[str, Any]:
    """由合并状态推导每条目的金额、生效标签与冲突列表。"""
    entries = []
    conflicts = []
    for entry_id in sorted(state["entries"]):
        entry_state = state["entries"][entry_id]
        amount_ops = entry_state["amount_ops"]
        distinct_amounts = sorted(set(amount_ops.values()), key=repr)
        if len(distinct_amounts) <= 1:
            amount = distinct_amounts[0] if distinct_amounts else entry_state["base_amount"]
        else:
            amount = None
            conflicts.append(
                {
                    "entry": entry_id,
                    "field": "amount",
                    "base": entry_state["base_amount"],
                    "values": {
                        op_id: amount_ops[op_id] for op_id in sorted(amount_ops)
                    },
                }
            )
        removed: set[str] = set()
        for rem in entry_state["tag_removes"].values():
            removed.update(rem["removes"])
        tags = sorted(
            {tag for op_id, tag in entry_state["tag_adds"].items() if op_id not in removed}
        )
        entries.append({"id": entry_id, "amount": amount, "tags": tags})
    return {"entries": entries, "conflicts": conflicts, "ok": not conflicts}


# ---------------------------------------------------------------------------
# 命令行
# ---------------------------------------------------------------------------

def _load_branch(spec: Any, base: dict[str, Any], name: str) -> dict[str, Any]:
    """分支输入可以是操作日志（list）或此前 ``--emit-state`` 输出的状态。"""
    if isinstance(spec, list):
        return apply_ops(base, spec)
    if isinstance(spec, dict) and "entries" in spec:
        return copy.deepcopy(spec)
    raise ValueError(
        f"{name}: expected a list of ops or a state object with an 'entries' key"
    )


def cmd_merge(args: argparse.Namespace) -> int:
    with open(args.input, "r", encoding="utf-8") as fh:
        doc = json.load(fh)
    base = base_state(doc["base"])
    left = _load_branch(doc.get("left", []), base, "left")
    right = _load_branch(doc.get("right", []), base, "right")
    merged = merge(base, left, right)
    result = resolve(merged)
    if args.emit_state:
        result["state"] = merged
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0 if result["ok"] else 1


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="merge_ledger",
        description="分支账本三方合并：不同字段自动合并，同字段金额冲突显式报告。",
    )
    sub = parser.add_subparsers(dest="command", required=True)
    merge_parser = sub.add_parser("merge", help="合并输入 JSON 并输出结果")
    merge_parser.add_argument("input", help="输入 JSON 文件（格式见 README.md）")
    merge_parser.add_argument(
        "--emit-state",
        action="store_true",
        help="在输出中附带内部合并状态（可作为后续合并的分支输入）",
    )
    args = parser.parse_args(argv)
    if args.command == "merge":
        try:
            return cmd_merge(args)
        except (ValueError, KeyError, json.JSONDecodeError, OSError) as exc:
            print(f"error: {exc}", file=sys.stderr)
            return 2
    return 2  # pragma: no cover


if __name__ == "__main__":
    sys.exit(main())
