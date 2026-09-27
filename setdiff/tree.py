"""分层哈希摘要树（前缀树 / Merkle 风格），用于按摘要定位差异子树。

结构：16 叉（每级一个十六进制字符）前缀树，深度可配置。
- 记录的路由键 = hasher.digest(key)，前 depth 个十六进制字符决定叶节点；
- 叶摘要 = H(逐条记录指纹的拼接)，记录指纹含 键/版本/删除标志/值，
  因此同一叶内键集合或内容不同会导致叶摘要不同（哈希无碰撞时）；
- 内部节点摘要 = H(16 个子摘要按序拼接)，空子树使用确定性的空摘要。

重要：摘要仅用于剪枝。恒定哈希下所有摘要相等，树无法区分任何差异，
这正是必须提供显式精确核验模式的原因。
"""

from __future__ import annotations

import hashlib
from typing import Iterable, Optional

from .hasher import DIGEST_HEX_LEN, Hasher
from .model import Record

FANOUT = 16  # 每个内部节点的子节点数（十六进制字符 0-f）
EMPTY_LEAF_SENTINEL = b"setdiff:empty-leaf:v1"


def _route_key_bytes(key: str) -> bytes:
    """路由键的规范字节：SHA-256(key) 的十六进制文本。

    路由固定使用 SHA-256，与可注入的内容摘要哈希器解耦，
    保证两端在恒定哈希下仍把同一键路由到同一叶节点。
    """
    return hashlib.sha256(key.encode("utf-8")).hexdigest().encode("ascii")


def record_fingerprint(hasher: Hasher, record: Record) -> str:
    """单条记录的内容指纹（参与叶摘要计算）。"""
    parts = [
        b"rec",
        len(record.key.encode("utf-8")).to_bytes(4, "big"),
        record.key.encode("utf-8"),
        record.version.to_bytes(8, "big"),
        b"\x01" if record.deleted else b"\x00",
    ]
    if record.value is not None:
        parts.append(len(record.value).to_bytes(4, "big"))
        parts.append(record.value)
    return hasher.digest(b"".join(parts))


class DigestTree:
    """固定深度的分层摘要树，惰性计算并缓存节点摘要。"""

    def __init__(self, records: Iterable[Record], hasher: Hasher, depth: int = 2) -> None:
        if depth < 1:
            raise ValueError("depth 必须 >= 1")
        self.hasher = hasher
        self.depth = depth
        self._leaves: dict[str, list[Record]] = {}
        for record in records:
            prefix = self.leaf_prefix(record.key)
            self._leaves.setdefault(prefix, []).append(record)
        for prefix in self._leaves:
            self._leaves[prefix].sort(key=lambda r: r.key)
        self._digest_cache: dict[str, str] = {}
        self._empty_cache: dict[int, str] = {}

    @staticmethod
    def child_prefixes(prefix: str) -> list[str]:
        """某节点的 16 个子节点前缀（按十六进制字符序）。"""
        return [prefix + format(i, "x") for i in range(FANOUT)]

    def leaf_prefix(self, key: str) -> str:
        """键所属叶节点的前缀。"""
        return _route_key_bytes(key)[: self.depth].decode("ascii")

    def leaf_records(self, prefix: str) -> list[Record]:
        """叶节点内的全部记录（按键排序）；空前缀返回空列表。"""
        if len(prefix) != self.depth:
            raise ValueError(f"叶前缀长度必须为 {self.depth}，得到 {prefix!r}")
        return list(self._leaves.get(prefix, []))

    def non_empty_leaf_prefixes(self) -> list[str]:
        return sorted(self._leaves)

    def _leaf_digest(self, prefix: str) -> str:
        records = self._leaves.get(prefix)
        if not records:
            return self._empty_digest(self.depth)
        fingerprints = "".join(record_fingerprint(self.hasher, r) for r in records)
        return self.hasher.digest(b"leaf:" + fingerprints.encode("ascii"))

    def _empty_digest(self, level: int) -> str:
        """深度为 level 的空子树的确定性摘要（自底向上缓存）。"""
        if level not in self._empty_cache:
            digest = self.hasher.digest(EMPTY_LEAF_SENTINEL)
            for _ in range(self.depth - level):
                digest = self.hasher.digest(b"node:" + digest * FANOUT)
            self._empty_cache[level] = digest
        return self._empty_cache[level]

    def node_digest(self, prefix: str = "") -> str:
        """任意节点的摘要；空前缀为根。"""
        if len(prefix) > self.depth:
            raise ValueError(f"前缀 {prefix!r} 超出树深 {self.depth}")
        if prefix in self._digest_cache:
            return self._digest_cache[prefix]
        if len(prefix) == self.depth:
            digest = self._leaf_digest(prefix)
        else:
            children = "".join(self.node_digest(child) for child in self.child_prefixes(prefix))
            digest = self.hasher.digest(b"node:" + children.encode("ascii"))
        self._digest_cache[prefix] = digest
        return digest

    def root_digest(self) -> str:
        return self.node_digest("")


def looks_degenerate(hasher: Hasher) -> bool:
    """探测哈希器是否对已知不同输入输出相同摘要（碰撞预警用）。"""
    probes = [b"setdiff-probe-0", b"setdiff-probe-1", b"", b"\x00" * 32]
    digests = {hasher.digest(probe) for probe in probes}
    return len(digests) < len(probes)
