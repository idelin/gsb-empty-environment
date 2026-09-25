"""回收器：标记代际、试算、提交验证、删除证书、存活证明与重放。"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from . import catalog
from .closure import Closure, compute_closure
from .model import canonical, content_hash, key_of, key_str
from .store import Store, StoreError


class CommitRejected(StoreError):
    """提交前版本/代际验证失败（R-CMT-1）。"""


@dataclass
class PlanResult:
    plan: dict[str, Any]
    closure: Closure

    def to_dict(self) -> dict[str, Any]:
        p = dict(self.plan)
        p["reclaimable"] = [key_str(k) for k in sorted(self.closure.reclaimable)]
        p["retained"] = [key_str(k) for k in sorted(self.closure.retained)]
        return p


def _rules_meta() -> dict[str, dict[str, int]]:
    return {rid: {"version": m["version"]} for rid, m in catalog.RULES.items()}


def _steps_meta() -> dict[str, dict[str, int]]:
    return {sid: {"version": m["version"]} for sid, m in catalog.STEPS.items()}


def preview(store: Store, now: int) -> dict[str, Any]:
    """试算（S1 的只读版本，R-DRY-1）：不写任何事件、不删除。"""
    closure = compute_closure(store, now)
    return {
        "kind": "gc.preview",
        "schema": catalog.SCHEMA_DOMAIN,
        "schema_version": catalog.SCHEMA_VERSION,
        "rule": {"id": "R-DRY-1", "version": catalog.RULES["R-DRY-1"]["version"]},
        "now": now,
        "dry_run": True,
        "reclaimable": [key_str(k) for k in sorted(closure.reclaimable)],
        "retained": [key_str(k) for k in sorted(closure.retained)],
        "expired": [key_str(k) for k in sorted(closure.expired)],
        "blocking_chains": closure.chain_dicts(),
        "rules": _rules_meta(),
    }


def _snapshot(store: Store) -> dict[str, Any]:
    return {
        "seq": store._seq,
        "generation": store.generation,
        "event_prefix_hash": content_hash(store.events),
        "instances": {
            key_str(k): ev.content_hash for k, ev in sorted(store.instances.items())
        },
        "current_versions": dict(sorted(store.current_version.items())),
    }


def create_plan(store: Store, now: int, *, tick: int) -> PlanResult:
    """S1 标记代际：固化版本快照与候选集。"""
    closure = compute_closure(store, now)
    gen = store.generation + 1
    token = f"gen{gen}-s{store._seq + 1}-{content_hash([now, store._seq, gen])[:10]}"
    candidates = [
        {"node": list(k), "node_id": key_str(k),
         "content_hash": store.instances[k].content_hash,
         "created_tick": store.instances[k].created_tick}
        for k in sorted(closure.reclaimable)
    ]
    plan = {
        "plan_token": token,
        "generation": gen,
        "planned_tick": tick,
        "eval_tick": now,
        "snapshot": _snapshot(store),
        "candidates": candidates,
        "retained": [list(k) for k in sorted(closure.retained)],
        "blocking_chains_at_plan": closure.chain_dicts(),
        "rule": {"id": "R-GC-1", "version": catalog.RULES["R-GC-1"]["version"]},
        "step": {"id": "S1", "version": catalog.STEPS["S1"]["version"]},
    }
    store.append_gc_planned(plan, tick=tick)
    return PlanResult(plan=plan, closure=closure)


def _categorize(chain: list[dict]) -> str:
    root_rule = chain[0]["rule"]
    if root_rule == "R-FRZ-1":
        return "freeze" if len(chain) == 1 else "freeze_inherited"
    if root_rule == "R-RET-1":
        return "retention" if len(chain) == 1 else "referenced"
    return root_rule


def build_liveness(store: Store, closure: Closure, generation: int) -> dict[str, Any]:
    """S5 存活证明（R-PRF-1）：每个存活实例有保留依据，且无悬空依赖。"""
    alive = set(store.instances)
    root_reasons: dict[str, list[str]] = {}
    for k in sorted(alive):
        reasons = [chain[0]["detail"] for chain in closure.chains.get(k, [])]
        root_reasons[key_str(k)] = sorted(set(reasons))
    missing_roots = [key_str(k) for k in sorted(alive) if k not in closure.chains]

    dangling: list[dict[str, Any]] = []
    edges: list[dict[str, Any]] = []
    for rid, ref in sorted(store.refs.items()):
        if not ref.active:
            continue
        rec = {"ref_id": rid, "source": list(ref.source), "target": list(ref.target)}
        if ref.target not in alive:
            dangling.append({**rec, "problem": "target_deleted"})
        elif ref.source not in alive:
            dangling.append({**rec, "problem": "source_deleted"})
        elif store.current_version.get(ref.target[0]) != ref.target[1]:
            dangling.append({**rec, "problem": "target_version_stale"})
        else:
            edges.append(rec)

    body = {
        "generation": generation,
        "survivors": sorted(root_reasons),
        "root_reasons": root_reasons,
        "valid_edges": sorted(edges, key=lambda e: e["ref_id"]),
        "dangling": dangling,
        "survivors_without_root": missing_roots,
        "rule": {"id": "R-PRF-1", "version": catalog.RULES["R-PRF-1"]["version"]},
    }
    body["proof_hash"] = content_hash(body)
    body["ok"] = not dangling and not missing_roots
    return body


def commit(store: Store, plan_token: str, now: int, *, tick: int) -> dict[str, Any]:
    """S2-S4 提交：版本验证 → 重算闭包 → 删除/阻止（R-CMT-1 / R-NEW-1）。"""
    plan = store.plans.get(plan_token)
    if plan is None:
        raise CommitRejected(f"计划不存在: {plan_token}")
    if store.is_plan_committed(plan_token):
        raise CommitRejected(f"计划已提交: {plan_token}")
    if store.generation != plan["generation"]:
        raise CommitRejected(
            f"代际过期（R-CMT-1）：计划 gen{plan['generation']}，当前 gen{store.generation}")

    candidate_keys = [key_of(*c["node"]) for c in plan["candidates"]]

    # S2 提交前版本验证：存在性 + 内容哈希
    missing: list[dict[str, Any]] = []
    for cand in plan["candidates"]:
        key = key_of(*cand["node"])
        ev = store.instances.get(key)
        if ev is None:
            missing.append({"node": list(key), "node_id": key_str(key),
                            "category": "missing", "chains": []})
        elif ev.content_hash != cand["content_hash"]:
            missing.append({"node": list(key), "node_id": key_str(key),
                            "category": "version_changed", "chains": []})

    # S3 解冻/新引用到来后重新计算闭包
    closure = compute_closure(store, now)

    blocked: list[dict[str, Any]] = list(missing)
    to_delete: list = []
    for key in candidate_keys:
        if any(b["node_id"] == key_str(key) for b in blocked):
            continue
        if key in closure.reclaimable:
            to_delete.append(key)
        else:
            chains = closure.chains.get(key, [])
            category = _categorize(chains[0]) if chains else "retained"
            blocked.append({
                "node": list(key),
                "node_id": key_str(key),
                "category": category,
                "chains": chains,
            })

    deleted = [{"node": list(k), "node_id": key_str(k),
                "content_hash": store.instances[k].content_hash}
               for k in sorted(to_delete)]

    commit_payload = {
        "plan_token": plan_token,
        "generation": plan["generation"],
        "committed_tick": tick,
        "eval_tick": now,
        "deleted": [d["node"] for d in deleted],
    }
    store.append_gc_committed(commit_payload, tick=tick)

    # S5 证书 + 存活证明
    post_closure = compute_closure(store, now)
    liveness = build_liveness(store, post_closure, plan["generation"])
    cert_id = "cert-" + content_hash([
        plan_token, tick,
        [d["node"] for d in deleted],
        [b["node_id"] for b in blocked],
    ])[:16]
    cert = {
        "kind": catalog.CERTIFICATE_KIND,
        "schema": catalog.SCHEMA_DOMAIN,
        "schema_version": catalog.SCHEMA_VERSION,
        "fact_type": {"id": "gc.certificate",
                      "version": catalog.FACT_TYPES["gc.certificate"]["version"]},
        "certificate_id": cert_id,
        "plan_token": plan_token,
        "generation": plan["generation"],
        "planned_tick": plan["planned_tick"],
        "committed_tick": tick,
        "eval_tick": now,
        "steps": _steps_meta(),
        "rules": _rules_meta(),
        "snapshot_at_plan": plan["snapshot"],
        "planned_candidates": plan["candidates"],
        "blocking_chains_at_plan": plan["blocking_chains_at_plan"],
        "deleted": deleted,
        "blocked": blocked,
        "liveness": liveness,
        "timeline": list(store.events),
        "replay": None,
    }
    cert["certificate_hash"] = content_hash(_hashable(cert))
    return cert


def _hashable(cert: dict[str, Any]) -> Any:
    out = {k: v for k, v in cert.items() if k not in ("certificate_hash", "replay")}
    return out


def replay_certificate(cert: dict[str, Any]) -> dict[str, Any]:
    """从时间线重放：重建存储 → 重新执行提交 → 比对证书（证据可重放）。"""
    timeline = cert["timeline"]
    if not timeline or timeline[-1]["type"] != "gc.committed":
        raise StoreError("时间线缺少 gc.committed 收尾事件")
    rebuilt = Store.from_events(timeline[:-1])
    token = cert["plan_token"]
    if token not in rebuilt.plans:
        raise StoreError("重放失败：时间线中找不到计划事件")
    replayed = commit(rebuilt, token, cert["eval_tick"], tick=cert["committed_tick"])
    expected_hash = cert["certificate_hash"]
    actual_hash = replayed["certificate_hash"]
    ok = expected_hash == actual_hash
    replayed["replay"] = {
        "verified": ok,
        "expected_certificate_hash": expected_hash,
        "actual_certificate_hash": actual_hash,
        "rule": {"id": "R-CMT-1", "version": catalog.RULES["R-CMT-1"]["version"]},
    }
    if not ok:
        raise StoreError("重放结果与证书不一致")
    return replayed
