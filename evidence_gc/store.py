"""事件溯源状态存储。

事件是唯一事实来源；状态由事件折叠（fold）得到，任何状态都可重放。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .model import InstanceKey, key_of, key_str


class StoreError(Exception):
    """存储/命令层错误基类。"""


class DuplicateError(StoreError):
    pass


class NotFoundError(StoreError):
    pass


class StaleTickError(StoreError):
    pass


@dataclass
class Evidence:
    eid: str
    version: int
    created_tick: int
    content_hash: str
    seq: int


@dataclass
class Ref:
    ref_id: str
    source: InstanceKey
    target: InstanceKey
    active: bool
    seq: int


@dataclass
class Freeze:
    freeze_id: str
    target: InstanceKey
    active: bool
    seq: int
    revoke_seq: int | None = None


@dataclass
class Policy:
    policy_id: str
    eid: str
    retention_ticks: int
    granted_tick: int
    seq: int


@dataclass
class Store:
    events: list[dict[str, Any]] = field(default_factory=list)
    _seq: int = field(default=0, init=False)
    instances: dict[InstanceKey, Evidence] = field(default_factory=dict, init=False)
    current_version: dict[str, int] = field(default_factory=dict, init=False)
    refs: dict[str, Ref] = field(default_factory=dict, init=False)
    freezes: dict[str, Freeze] = field(default_factory=dict, init=False)
    policies: dict[str, Policy] = field(default_factory=dict, init=False)
    plans: dict[str, dict[str, Any]] = field(default_factory=dict, init=False)
    committed_plans: set[str] = field(default_factory=set, init=False)
    generation: int = field(default=0, init=False)

    # ------------------------------------------------------------------
    # 追加与折叠
    # ------------------------------------------------------------------
    def _append(self, etype: str, tick: int, payload: dict[str, Any]) -> dict[str, Any]:
        if self.events and tick < self.events[-1]["tick"]:
            raise StaleTickError(f"tick 必须单调不减: {tick} < {self.events[-1]['tick']}")
        self._seq += 1
        event = {"seq": self._seq, "type": etype, "tick": tick, "payload": payload}
        self._apply(event)
        self.events.append(event)
        return event

    def _apply(self, event: dict[str, Any]) -> None:
        seq, etype, tick, p = event["seq"], event["type"], event["tick"], event["payload"]
        if etype == "evidence.registered":
            k = key_of(p["eid"], p["version"])
            self.instances[k] = Evidence(p["eid"], p["version"], p["created_tick"],
                                         p["content_hash"], seq)
            cur = self.current_version.get(p["eid"])
            if cur is None or p["version"] > cur:
                self.current_version[p["eid"]] = p["version"]
        elif etype == "evidence.refed":
            src, tgt = tuple(p["source"]), tuple(p["target"])
            self.refs[p["ref_id"]] = Ref(p["ref_id"], src, tgt, True, seq)
        elif etype == "reference.revoked":
            self.refs[p["ref_id"]].active = False
        elif etype == "freeze.placed":
            tgt = tuple(p["target"])
            self.freezes[p["freeze_id"]] = Freeze(p["freeze_id"], tgt, True, seq)
        elif etype == "freeze.revoked":
            fr = self.freezes[p["freeze_id"]]
            fr.active = False
            fr.revoke_seq = seq
        elif etype == "retention.granted":
            existing = self.policies.get(p["policy_id"])
            if existing is not None and existing.eid != p["eid"]:
                raise StoreError(f"保留策略 {p['policy_id']} 已绑定其他证据")
            self.policies[p["policy_id"]] = Policy(
                p["policy_id"], p["eid"], p["retention_ticks"], p["granted_tick"], seq
            )
        elif etype == "gc.planned":
            self.generation = max(self.generation, p["generation"])
            self.plans[p["plan_token"]] = p
        elif etype == "gc.committed":
            token = p["plan_token"]
            for k in [tuple(x) for x in p["deleted"]]:
                self.instances.pop(k, None)
                ref_ids = [rid for rid, r in self.refs.items()
                           if r.source == k or r.target == k]
                for rid in ref_ids:
                    del self.refs[rid]
                fids = [fid for fid, f in self.freezes.items() if f.target == k]
                for fid in fids:
                    del self.freezes[fid]
                if self.current_version.get(k[0]) == k[1]:
                    survivors = [x for x in self.instances if x[0] == k[0]]
                    if survivors:
                        self.current_version[k[0]] = max(x[1] for x in survivors)
                    else:
                        del self.current_version[k[0]]
            self.committed_plans.add(token)
        else:
            raise StoreError(f"未知事件类型: {etype}")

    @classmethod
    def from_events(cls, events: list[dict[str, Any]]) -> "Store":
        store = cls()
        for ev in events:
            store._seq = max(store._seq, ev["seq"] - 1)
            store._apply(ev)
            store.events.append(ev)
        if events:
            store._seq = max(ev["seq"] for ev in events)
        return store

    # ------------------------------------------------------------------
    # 命令（均做版本/存在性验证）
    # ------------------------------------------------------------------
    def add_evidence(self, eid: str, version: int, created_tick: int,
                     content_hash: str, *, tick: int) -> dict[str, Any]:
        k = key_of(eid, version)
        if k in self.instances:
            raise DuplicateError(f"证据实例已存在: {key_str(k)}")
        return self._append("evidence.registered", tick, {
            "eid": eid, "version": version, "created_tick": created_tick,
            "content_hash": content_hash,
        })

    def add_reference(self, ref_id: str, source: InstanceKey, target: InstanceKey,
                      *, tick: int) -> dict[str, Any]:
        if ref_id in self.refs:
            raise DuplicateError(f"引用已存在: {ref_id}")
        if source not in self.instances:
            raise NotFoundError(f"引用源不存在: {key_str(source)}")
        if target not in self.instances:
            raise NotFoundError(f"引用目标不存在: {key_str(target)}（拒绝悬空依赖）")
        return self._append("evidence.refed", tick, {
            "ref_id": ref_id, "source": list(source), "target": list(target),
        })

    def revoke_reference(self, ref_id: str, *, tick: int) -> dict[str, Any]:
        if ref_id not in self.refs:
            raise NotFoundError(f"引用不存在: {ref_id}")
        if not self.refs[ref_id].active:
            raise StoreError(f"引用已失效: {ref_id}")
        return self._append("reference.revoked", tick, {"ref_id": ref_id})

    def place_freeze(self, freeze_id: str, target: InstanceKey, *, tick: int) -> dict[str, Any]:
        if freeze_id in self.freezes:
            raise DuplicateError(f"冻结标记已存在: {freeze_id}")
        if target not in self.instances:
            raise NotFoundError(f"冻结目标不存在: {key_str(target)}")
        return self._append("freeze.placed", tick, {
            "freeze_id": freeze_id, "target": list(target),
        })

    def revoke_freeze(self, freeze_id: str, *, tick: int) -> dict[str, Any]:
        if freeze_id not in self.freezes:
            raise NotFoundError(f"冻结标记不存在: {freeze_id}")
        if not self.freezes[freeze_id].active:
            raise StoreError(f"冻结已撤回: {freeze_id}")
        return self._append("freeze.revoked", tick, {"freeze_id": freeze_id})

    def grant_retention(self, policy_id: str, eid: str, retention_ticks: int,
                        *, tick: int) -> dict[str, Any]:
        if eid not in self.current_version:
            raise NotFoundError(f"证据不存在: {eid}")
        return self._append("retention.granted", tick, {
            "policy_id": policy_id, "eid": eid,
            "retention_ticks": retention_ticks, "granted_tick": tick,
        })

    # ------------------------------------------------------------------
    # GC 计划/提交事件（由 reaper 计算后写入）
    # ------------------------------------------------------------------
    def append_gc_planned(self, plan: dict[str, Any], *, tick: int) -> dict[str, Any]:
        if plan["plan_token"] in self.plans:
            raise DuplicateError(f"计划已存在: {plan['plan_token']}")
        return self._append("gc.planned", tick, plan)

    def append_gc_committed(self, payload: dict[str, Any], *, tick: int) -> dict[str, Any]:
        token = payload["plan_token"]
        if token not in self.plans:
            raise NotFoundError(f"计划不存在: {token}")
        if token in self.committed_plans:
            raise StoreError(f"计划已提交: {token}")
        return self._append("gc.committed", tick, payload)

    def is_plan_committed(self, token: str) -> bool:
        return token in self.committed_plans
