"""最小不可回收闭包与引用阻塞链（规则 R-CLO-1 / R-RET-1 / R-REF-1 / R-FRZ-1）。"""

from __future__ import annotations

from collections import defaultdict, deque
from dataclasses import dataclass, field

from .model import InstanceKey, key_str
from .store import Store


@dataclass
class Closure:
    now: int
    retained: set[InstanceKey] = field(default_factory=set)
    reclaimable: set[InstanceKey] = field(default_factory=set)
    # key -> 阻塞链列表；一条链对应一种根因（保留期/冻结）
    chains: dict[InstanceKey, list[list[dict]]] = field(default_factory=lambda: defaultdict(list))
    valid_edges: list[tuple[str, InstanceKey, InstanceKey]] = field(default_factory=list)
    expired: set[InstanceKey] = field(default_factory=set)
    root_rules: dict[InstanceKey, list[str]] = field(default_factory=lambda: defaultdict(list))

    def chain_dicts(self) -> dict[str, list[list[dict]]]:
        return {key_str(k): v for k, v in self.chains.items()}


def _retention_for(store: Store, eid: str) -> int:
    return max((p.retention_ticks for p in store.policies.values() if p.eid == eid), default=0)


def compute_valid_edges(store: Store) -> list[tuple[str, InstanceKey, InstanceKey]]:
    """有效引用：active 且 target_version == 目标当前版本（R-REF-1）。"""
    edges: list[tuple[str, InstanceKey, InstanceKey]] = []
    for ref in store.refs.values():
        if not ref.active:
            continue
        tgt_eid, tgt_ver = ref.target
        if store.current_version.get(tgt_eid) != tgt_ver:
            continue
        if ref.source in store.instances and ref.target in store.instances:
            edges.append((ref.ref_id, ref.source, ref.target))
    return edges


def compute_closure(store: Store, now: int) -> Closure:
    """求最小不可回收闭包（满足 R-RET-1/R-FRZ-1 根 + 沿有效引用闭包的最小不动点）。"""
    result = Closure(now=now)
    result.valid_edges = compute_valid_edges(store)
    outgoing: dict[InstanceKey, list[tuple[str, InstanceKey]]] = defaultdict(list)
    for rid, src, tgt in result.valid_edges:
        outgoing[src].append((rid, tgt))

    active_freeze_targets = {f.target for f in store.freezes.values() if f.active}

    # BFS：按根因类型分别传播，保留最短链；同一节点可有多种根因。
    # 状态: (node, kind) 已访问，kind in {"ret","frz"}
    queue: deque[tuple[InstanceKey, str, list[dict]]] = deque()
    seen: set[tuple[InstanceKey, str]] = set()

    def seed(node: InstanceKey, kind: str, root_rule: str, detail: str) -> None:
        if (node, kind) in seen:
            return
        seen.add((node, kind))
        chain = [{
            "kind": "root",
            "node": list(node),
            "rule": root_rule,
            "detail": detail,
        }]
        queue.append((node, kind, chain))

    for key, ev in sorted(store.instances.items()):
        retention = _retention_for(store, key[0])
        expire_at = ev.created_tick + retention
        if expire_at <= now:
            result.expired.add(key)
        # R-RET-1：created_tick + retention_ticks > now
        if expire_at > now:
            result.root_rules[key].append("R-RET-1")
            seed(key, "ret", "R-RET-1",
                 f"自定义保留期至 tick {expire_at}（now={now}，法定保留不适用）")
        # R-FRZ-1：显式冻结标记
        if key in active_freeze_targets:
            fids = sorted(f.freeze_id for f in store.freezes.values()
                          if f.active and f.target == key)
            result.root_rules[key].append("R-FRZ-1")
            seed(key, "frz", "R-FRZ-1", f"显式冻结标记: {', '.join(fids)}")

    while queue:
        node, kind, chain = queue.popleft()
        result.retained.add(node)
        result.chains[node].append(chain)
        for rid, tgt in outgoing.get(node, []):
            if (tgt, kind) in seen:
                continue
            seen.add((tgt, kind))
            if kind == "frz":
                hop_rule, hop_kind, detail = "R-FRZ-1", "freeze_inherit", f"冻结沿有效引用 {rid} 继承"
            else:
                hop_rule, hop_kind, detail = "R-REF-1", "ref", f"活结论经有效引用 {rid} 钉住版本"
            new_chain = chain + [{
                "kind": hop_kind,
                "node": list(tgt),
                "rule": hop_rule,
                "ref_id": rid,
                "detail": detail,
            }]
            queue.append((tgt, kind, new_chain))

    result.reclaimable = {k for k in store.instances if k not in result.retained}
    return result
