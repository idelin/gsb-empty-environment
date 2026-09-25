"""事件日志：追加、准入校验、折叠重放。"""

from __future__ import annotations

import copy
from typing import Any, Dict, List

from .facts import (
    Event,
    FreezeMarker,
    NodeKey,
    NodeVersion,
    Reference,
    Store,
    make_event,
)

NODE_ADD = "node.add"
REF_ADD = "ref.add"
REF_REVOKE = "ref.revoke"
FREEZE_ADD = "freeze.add"
FREEZE_REVOKE = "freeze.revoke"
RECLAIM_COMMIT = "reclaim.commit"


class EventError(ValueError):
    """事件准入或折叠失败（含悬空依赖检查）。"""


def _node_from_body(body: Dict[str, Any]) -> NodeVersion:
    return NodeVersion(
        evidence_id=body["evidence_id"],
        version=body["version"],
        created_at=body["created_at"],
        retention_seconds=body["retention_seconds"],
        payload=body["payload"],
    )


def _ref_from_body(body: Dict[str, Any]) -> Reference:
    return Reference(
        ref_id=body["ref_id"],
        source=(body["source"][0], body["source"][1]),
        target=(body["target"][0], body["target"][1]),
        created_at=body["created_at"],
        live=body.get("live", True),
    )


def _freeze_from_body(body: Dict[str, Any]) -> FreezeMarker:
    return FreezeMarker(
        freeze_id=body["freeze_id"],
        target=(body["target"][0], body["target"][1]),
        created_at=body["created_at"],
        active=body.get("active", True),
    )


def apply_event(store: Store, kind: str, body: Dict[str, Any]) -> None:
    """把单个事件折叠进存储；非法事件抛出 EventError。"""

    if kind == NODE_ADD:
        node = _node_from_body(body)
        if node.version < 1:
            raise EventError("节点版本必须 >= 1")
        if node.key() in store.nodes:
            raise EventError(f"节点版本已存在: {node.key()}")
        store.nodes[node.key()] = node

    elif kind == REF_ADD:
        ref = _ref_from_body(body)
        if ref.ref_id in store.refs:
            raise EventError(f"引用 ID 已存在: {ref.ref_id}")
        if ref.source not in store.nodes:
            raise EventError(f"引用源不存在或已回收（悬空）: {ref.source}")
        if ref.target not in store.nodes:
            raise EventError(f"引用目标不存在或已回收（悬空）: {ref.target}")
        store.refs[ref.ref_id] = ref

    elif kind == REF_REVOKE:
        ref_id = body["ref_id"]
        ref = store.refs.get(ref_id)
        if ref is None:
            raise EventError(f"引用不存在: {ref_id}")
        if not ref.live:
            raise EventError(f"引用已撤销: {ref_id}")
        store.refs[ref_id] = Reference(
            ref_id=ref.ref_id,
            source=ref.source,
            target=ref.target,
            created_at=ref.created_at,
            live=False,
        )

    elif kind == FREEZE_ADD:
        marker = _freeze_from_body(body)
        if marker.freeze_id in store.freezes:
            raise EventError(f"冻结 ID 已存在: {marker.freeze_id}")
        if marker.target not in store.nodes:
            raise EventError(f"冻结目标不存在或已回收: {marker.target}")
        store.freezes[marker.freeze_id] = marker

    elif kind == FREEZE_REVOKE:
        freeze_id = body["freeze_id"]
        marker = store.freezes.get(freeze_id)
        if marker is None:
            raise EventError(f"冻结不存在: {freeze_id}")
        if not marker.active:
            raise EventError(f"冻结已撤回: {freeze_id}")
        store.freezes[freeze_id] = FreezeMarker(
            freeze_id=marker.freeze_id,
            target=marker.target,
            created_at=marker.created_at,
            active=False,
        )

    elif kind == RECLAIM_COMMIT:
        cert_id = body["cert_id"]
        if cert_id in store.reclaim_commits:
            raise EventError(f"删除证书已存在: {cert_id}")
        deleted_nodes = [tuple(k) for k in body["deleted_nodes"]]
        deleted_refs = list(body["deleted_refs"])
        for key in deleted_nodes:
            if key not in store.nodes:
                raise EventError(f"提交失败：候选节点已不存在 {key}")
        for rid in deleted_refs:
            if rid not in store.refs:
                raise EventError(f"提交失败：引用已不存在 {rid}")
        for key in deleted_nodes:
            del store.nodes[key]  # type: ignore[arg-type]
        for rid in deleted_refs:
            del store.refs[rid]
        store.reclaim_commits[cert_id] = {
            "schema": body.get("schema", "reclaim-cert.v1"),
            "cert_id": cert_id,
            "generation": body["generation"],
            "deleted_nodes": [list(k) for k in deleted_nodes],
        }

    else:
        raise EventError(f"未知事件类型: {kind}")

    store.version += 1


class EventLog:
    """只追加事件日志与物化存储。"""

    def __init__(self) -> None:
        self.events: List[Event] = []
        self.store = Store()

    @property
    def version(self) -> int:
        return self.store.version

    def append(self, kind: str, body: Dict[str, Any]) -> Event:
        """校验并追加事件，返回带稳定 ID 的事件。"""

        # 先在临时存储上校验，保证失败时日志不被部分修改
        trial = copy.deepcopy(self.store)
        apply_event(trial, kind, body)
        event = make_event(len(self.events) + 1, kind, body)
        apply_event(self.store, kind, body)
        self.events.append(event)
        return event

    def clone(self) -> "EventLog":
        new = EventLog()
        new.events = copy.deepcopy(self.events)
        new.store = copy.deepcopy(self.store)
        return new

    def event_records(self) -> List[Dict[str, Any]]:
        return [e.to_dict() for e in self.events]

    @classmethod
    def from_records(cls, records: List[Dict[str, Any]]) -> "EventLog":
        """从事件记录完整重建（重放）。"""

        log = cls()
        for rec in records:
            event = Event(
                event_id=rec["event_id"],
                seq=rec["seq"],
                kind=rec["kind"],
                body=rec["body"],
            )
            if event.seq != log.version + 1:
                raise EventError(f"事件序号不连续: 期望 {log.version + 1}，得到 {event.seq}")
            if event != make_event(event.seq, event.kind, event.body):
                raise EventError(f"事件稳定 ID 与内容不符: seq={event.seq}")
            apply_event(log.store, event.kind, event.body)
            log.events.append(event)
        return log
