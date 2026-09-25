"""事实模型：节点版本、引用、冻结标记、事件。

每个事实均带稳定 ID 与 schema 版本（*_V1）。
仓库为事件溯源：状态由事件日志折叠而来，可完整重放。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, Optional, Tuple

from .ids import canonical, digest, stable_id

SCHEMA_VERSION = 1
NODE_V1 = "node.v1"
REF_V1 = "ref.v1"
FREEZE_V1 = "freeze.v1"
EVENT_V1 = "event.v1"

NodeKey = Tuple[str, int]  # (evidence_id, version)


@dataclass(frozen=True)
class NodeVersion:
    """证据节点的一个版本。"""

    evidence_id: str
    version: int
    created_at: int
    retention_seconds: Optional[int]  # None 表示法定无关的自定义保留期未设置 -> 永久保留
    payload: str

    def key(self) -> NodeKey:
        return (self.evidence_id, self.version)

    def expires_at(self) -> Optional[int]:
        if self.retention_seconds is None:
            return None
        return self.created_at + self.retention_seconds

    def is_expired(self, now: int) -> bool:
        exp = self.expires_at()
        return exp is not None and now >= exp

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": NODE_V1,
            "evidence_id": self.evidence_id,
            "version": self.version,
            "created_at": self.created_at,
            "retention_seconds": self.retention_seconds,
            "payload": self.payload,
        }

    def stable_id(self) -> str:
        return stable_id("node", self.to_dict())


@dataclass(frozen=True)
class Reference:
    """一条有向引用（可撤销）。存活结论的引用即“有效引用”。"""

    ref_id: str
    source: NodeKey
    target: NodeKey
    created_at: int
    live: bool = True  # False 表示已被撤销，不再提供保护

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": REF_V1,
            "ref_id": self.ref_id,
            "source": list(self.source),
            "target": list(self.target),
            "created_at": self.created_at,
            "live": self.live,
        }

    def stable_id(self) -> str:
        return stable_id("ref", self.to_dict())


@dataclass(frozen=True)
class FreezeMarker:
    """显式冻结标记：可继承（沿有效引用传播）、可撤回。"""

    freeze_id: str
    target: NodeKey
    created_at: int
    active: bool = True

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": FREEZE_V1,
            "freeze_id": self.freeze_id,
            "target": list(self.target),
            "created_at": self.created_at,
            "active": self.active,
        }

    def stable_id(self) -> str:
        return stable_id("freeze", self.to_dict())


@dataclass(frozen=True)
class Event:
    """事件日志条目。seq 即存储版本（单调递增）。"""

    event_id: str
    seq: int
    kind: str
    body: Dict[str, Any]

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": EVENT_V1,
            "event_id": self.event_id,
            "seq": self.seq,
            "kind": self.kind,
            "body": self.body,
        }

    def stable_id(self) -> str:
        return stable_id("event", self.to_dict())


def make_event(seq: int, kind: str, body: Dict[str, Any]) -> Event:
    """构造事件并派生稳定 event_id（与 seq 绑定，保证日志唯一）。"""
    event_id = stable_id("event", {"kind": kind, "seq": seq, "body": body})
    return Event(event_id=event_id, seq=seq, kind=kind, body=body)


@dataclass
class Store:
    """由事件日志折叠出的物化状态。version 为已应用事件数。"""

    nodes: Dict[NodeKey, NodeVersion] = field(default_factory=dict)
    refs: Dict[str, Reference] = field(default_factory=dict)
    freezes: Dict[str, FreezeMarker] = field(default_factory=dict)
    reclaim_commits: Dict[str, Dict[str, Any]] = field(default_factory=dict)
    version: int = 0

    def live_refs(self) -> Dict[str, Reference]:
        return {rid: r for rid, r in self.refs.items() if r.live}

    def state_root(self) -> str:
        """状态根哈希：用于存活证明与重放校验。"""
        return digest(
            {
                "nodes": sorted((v.to_dict() for v in self.nodes.values()), key=canonical),
                "refs": sorted((r.to_dict() for r in self.refs.values()), key=canonical),
                "freezes": sorted((f.to_dict() for f in self.freezes.values()), key=canonical),
                "reclaim_commits": sorted(self.reclaim_commits.keys()),
            }
        )
