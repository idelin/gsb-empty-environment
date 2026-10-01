"""解释树：节点结构、规范顺序与 Merkle 哈希。

- 每个节点携带语义负载；``hash`` 为 Merkle 哈希（负载 + 子节点哈希），
  用于完整性识别。验证器绝不信任该哈希或任何检索器标签，
  真正的判定来自对证据（文档历史版本 + 统计快照 + 规则版本）的复算。
- 子节点规范顺序：result 按 rank 升序，其余按 (类型, doc_id)。
  验证器按此顺序逐节点复算，报告首个不成立节点。
"""

from __future__ import annotations

import hashlib

from .store import canonical_json

_TYPE_ORDER = {"result": 0, "filtered": 1, "no_match": 2, "pruned": 3, "scored_out": 4}


def child_key(node: dict):
    if node["type"] == "result":
        return (0, node["rank"], "")
    return (_TYPE_ORDER[node["type"]], 0, node["doc_id"])


def node_hash(node: dict) -> str:
    payload = {k: v for k, v in node.items() if k not in ("hash", "children")}
    child_hashes = [node_hash(c) for c in node.get("children", [])]
    blob = canonical_json({"payload": payload, "children": child_hashes})
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def finalize(node: dict) -> dict:
    """自底向上为整棵树写入 Merkle 哈希。"""
    for child in node.get("children", []):
        finalize(child)
    node["hash"] = node_hash(node)
    return node
