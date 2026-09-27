"""增量集合差异证明（incremental set reconciliation with hierarchical digests）。

两端各维护 键 -> (版本, 值) 映射以及删除墓碑。通过分层哈希摘要树定位差异。

两种模式：
  - probabilistic（概率模式）：摘要相等即跳过子树。若哈希被注入为恒定值（碰撞），
    同键不同值会被漏报。传输量小，正确性依赖哈希抗碰撞性。
  - exact（精确/确定模式）：不信任任何摘要，始终下钻到叶子并逐条精确比对。
    即使哈希恒定也必然报告全部差异，代价是传输全部条目。

合并规则（确定性）：
  1. 版本高者胜；
  2. 版本相同，墓碑（删除）胜存活值；
  3. 双方均存活且版本相同，取字典序较大的值（仅为确定性，业务上不应发生）。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from dataclasses import dataclass, field
from typing import Callable, Optional

TREE_DEPTH = 8          # 键哈希前缀的十六进制位数（树深）
FANOUT = 16             # 每层分支因子（一个十六进制位）


# ---------------------------------------------------------------- 哈希函数

def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()

def _constant(data: bytes) -> str:
    return "0" * 64

HASH_FUNCTIONS: dict[str, Callable[[bytes], str]] = {
    "sha256": _sha256,
    "constant": _constant,  # 仅供测试：强制全部碰撞
}


# ---------------------------------------------------------------- 数据模型

def live_entry(key: str, version: int, value: str) -> dict:
    return {"key": key, "version": version, "value": value}

def tombstone_entry(key: str, version: int) -> dict:
    return {"key": key, "version": version, "deleted": True}

def is_tombstone(entry: dict) -> bool:
    return bool(entry.get("deleted"))

def canonical(entry: dict) -> str:
    return json.dumps(entry, sort_keys=True, ensure_ascii=False)


class Store:
    """一端的状态：存活条目 + 删除墓碑。"""

    def __init__(self) -> None:
        self.entries: dict[str, dict] = {}   # key -> live entry
        self.tombstones: dict[str, dict] = {}  # key -> tombstone entry

    def put(self, key: str, version: int, value: str) -> None:
        existing = self.tombstones.get(key)
        if existing and existing["version"] >= version:
            return  # 墓碑版本不低于新值，保持删除
        self.tombstones.pop(key, None)
        cur = self.entries.get(key)
        if cur is None or cur["version"] <= version:
            self.entries[key] = live_entry(key, version, value)

    def delete(self, key: str, version: int) -> None:
        cur = self.entries.get(key)
        if cur and cur["version"] > version:
            return  # 存活值版本更高，墓碑不生效
        self.entries.pop(key, None)
        old = self.tombstones.get(key)
        if old is None or old["version"] < version:
            self.tombstones[key] = tombstone_entry(key, version)

    def all_records(self) -> list[dict]:
        return sorted(
            list(self.entries.values()) + list(self.tombstones.values()),
            key=lambda e: e["key"],
        )

    @classmethod
    def from_json(cls, doc: dict) -> "Store":
        store = cls()
        for e in doc.get("entries", []):
            store.put(e["key"], int(e["version"]), e["value"])
        for t in doc.get("tombstones", []):
            store.delete(t["key"], int(t["version"]))
        return store

    def to_json(self) -> dict:
        return {
            "entries": [self.entries[k] for k in sorted(self.entries)],
            "tombstones": [self.tombstones[k] for k in sorted(self.tombstones)],
        }


def resolve(a: Optional[dict], b: Optional[dict]) -> Optional[dict]:
    """确定性合并规则：版本高者胜；同版本墓碑胜；同版本同存活取较大值。"""
    if a is None:
        return b
    if b is None:
        return a
    if a["version"] != b["version"]:
        return a if a["version"] > b["version"] else b
    if is_tombstone(a) != is_tombstone(b):
        return a if is_tombstone(a) else b
    if is_tombstone(a):
        return a
    return a if a["value"] >= b["value"] else b


def merged_store(sa: Store, sb: Store) -> Store:
    """对两端的全部键应用合并规则，得到收敛后的存储。"""
    out = Store()
    keys = set(sa.entries) | set(sa.tombstones) | set(sb.entries) | set(sb.tombstones)
    for key in keys:
        ra = sa.entries.get(key) or sa.tombstones.get(key)
        rb = sb.entries.get(key) or sb.tombstones.get(key)
        winner = resolve(ra, rb)
        if winner is None:
            continue
        if is_tombstone(winner):
            out.tombstones[key] = dict(winner)
        else:
            out.entries[key] = dict(winner)
    return out


# ---------------------------------------------------------------- 摘要树

@dataclass
class Node:
    depth: int
    digest: str = ""
    children: dict = field(default_factory=dict)  # int -> Node
    entries: list = field(default_factory=list)   # 仅叶子（depth == TREE_DEPTH）

    @property
    def is_leaf(self) -> bool:
        return self.depth == TREE_DEPTH


def build_tree(records: list[dict], hash_fn: Callable[[bytes], str],
               depth: int = TREE_DEPTH) -> Node:
    root = Node(depth=0)
    for rec in records:
        path = hash_fn(rec["key"].encode("utf-8"))
        node = root
        for d in range(depth):
            idx = int(path[d], 16)
            node = node.children.setdefault(idx, Node(depth=d + 1))
        node.entries.append(rec)

    def finalize(node: Node) -> str:
        if node.is_leaf:
            payload = "L|" + "|".join(canonical(e) for e in
                                      sorted(node.entries, key=lambda e: e["key"]))
        else:
            parts = [f"{i:x}:{finalize(c)}" for i, c in sorted(node.children.items())]
            payload = "I|" + "|".join(parts)
        node.digest = hash_fn(payload.encode("utf-8"))
        return node.digest

    finalize(root)
    return root


# ---------------------------------------------------------------- 同步协议

@dataclass
class Stats:
    mode: str
    digest_comparisons: int = 0
    nodes_visited: int = 0
    entries_transmitted: int = 0  # 协议消息中携带的条目总数（双向）

    def to_json(self) -> dict:
        return {
            "mode": self.mode,
            "digest_comparisons": self.digest_comparisons,
            "nodes_visited": self.nodes_visited,
            "entries_transmitted": self.entries_transmitted,
        }


def _compare_leaves(ea: list[dict], eb: list[dict]) -> list[dict]:
    """逐键精确比对两个叶子的条目，产出可复核的差异列表。"""
    diffs = []
    ma = {e["key"]: e for e in ea}
    mb = {e["key"]: e for e in eb}
    for key in sorted(set(ma) | set(mb)):
        ra, rb = ma.get(key), mb.get(key)
        if ra is not None and rb is not None and canonical(ra) == canonical(rb):
            continue
        winner = resolve(ra, rb)
        diffs.append({
            "key": key,
            "local": ra,
            "remote": rb,
            "resolution": winner,
        })
    return diffs


def sync(sa: Store, sb: Store, mode: str = "exact",
         hash_name: str = "sha256", depth: int = TREE_DEPTH) -> dict:
    """执行一次同步，返回差异列表、合并结果与传输统计。"""
    if mode not in ("exact", "probabilistic"):
        raise ValueError(f"unknown mode: {mode}")
    hash_fn = HASH_FUNCTIONS[hash_name]
    ta = build_tree(sa.all_records(), hash_fn, depth)
    tb = build_tree(sb.all_records(), hash_fn, depth)
    stats = Stats(mode=mode)
    diffs: list[dict] = []

    def walk(na: Optional[Node], nb: Optional[Node]) -> None:
        if na is None and nb is None:
            return
        stats.nodes_visited += 1
        if na is not None and nb is not None:
            stats.digest_comparisons += 1
            if mode == "probabilistic" and na.digest == nb.digest:
                return  # 概率模式：信任摘要，跳过子树（可能被碰撞欺骗）
        # exact 模式不信任摘要，始终下钻；probabilistic 模式仅在摘要不同时下钻
        a_leaf = na is None or na.is_leaf
        b_leaf = nb is None or nb.is_leaf
        if a_leaf and b_leaf:
            ea = na.entries if na else []
            eb = nb.entries if nb else []
            stats.entries_transmitted += len(ea) + len(eb)
            diffs.extend(_compare_leaves(ea, eb))
            return
        indices = set()
        if na is not None:
            indices |= set(na.children)
        if nb is not None:
            indices |= set(nb.children)
        for i in sorted(indices):
            walk(na.children.get(i) if na else None,
                 nb.children.get(i) if nb else None)

    walk(ta, tb)
    return {
        "mode": mode,
        "hash": hash_name,
        "differences": diffs,
        "merged": merged_store(sa, sb).to_json(),
        "stats": stats.to_json(),
        "mode_semantics": (
            "exact: 确定性，逐条核验，不受哈希碰撞影响；"
            "probabilistic: 摘要相等即视为相同，碰撞时可能漏报差异"
        ),
    }


# ---------------------------------------------------------------- CLI

def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        description="增量集合差异证明：基于分层哈希摘要的集合同步")
    parser.add_argument("local", help="本地状态 JSON 文件")
    parser.add_argument("remote", help="对端状态 JSON 文件")
    parser.add_argument("--mode", choices=["exact", "probabilistic"],
                        default="exact", help="exact=确定模式（默认），probabilistic=概率模式")
    parser.add_argument("--hash", choices=sorted(HASH_FUNCTIONS), default="sha256",
                        help="摘要哈希函数；constant 仅供测试碰撞")
    args = parser.parse_args(argv)

    with open(args.local, encoding="utf-8") as f:
        sa = Store.from_json(json.load(f))
    with open(args.remote, encoding="utf-8") as f:
        sb = Store.from_json(json.load(f))

    result = sync(sa, sb, mode=args.mode, hash_name=args.hash)
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
