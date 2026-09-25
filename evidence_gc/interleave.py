"""交错枚举：引用新增 × 解冻 × 回收提交 的全排列。

验证核心保证（R-NEW-1）：提交前到来的新引用阻止已计划删除；
提交后到来的引用因目标已删除而被拒绝（无悬空依赖）。
"""

from __future__ import annotations

import itertools
from typing import Any

from .model import content_hash, key_str, synthetic_content
from .reaper import commit, create_plan
from .store import Store, StoreError


def build_base() -> tuple[Store, str]:
    """合成基础场景（全合成数据）：

    - X 保留期 1000（存活根，R-RET-1）
    - Y 保留期 10 且被冻结 F1（R-FRZ-1）
    - Z 保留期 10，过期可回收 → gen1 唯一候选
    """
    store = Store()
    for eid, retention in (("X", 1000), ("Y", 10), ("Z", 10)):
        store.add_evidence(eid, 1, 0, content_hash(synthetic_content(eid, 1)), tick=0)
        store.grant_retention(f"P-{eid}", eid, retention, tick=0)
    store.place_freeze("F1", ("Y", 1), tick=1)
    plan = create_plan(store, 50, tick=50)
    return store, plan.plan["plan_token"]


def _run_op(store: Store, token: str, name: str) -> str:
    if name == "add_ref":
        store.add_reference("R-XZ", ("X", 1), ("Z", 1), tick=60)
        return "引用新增 R-XZ: X@v1 -> Z@v1（已登记）"
    if name == "unfreeze":
        store.revoke_freeze("F1", tick=60)
        return "解冻 F1（目标 Y@v1，已撤回）"
    if name == "commit":
        cert = commit(store, token, 60, tick=60)
        deleted = ",".join(d["node_id"] for d in cert["deleted"]) or "无"
        blocked = ",".join(b["node_id"] for b in cert["blocked"]) or "无"
        return f"回收提交 gen{cert['generation']}: 删除[{deleted}] 阻止[{blocked}]"
    raise ValueError(name)


def enumerate_interleavings() -> list[dict[str, Any]]:
    """枚举 3! = 6 种交错；出错的操作记录拒绝原因后继续，最后统一收尾清理。"""
    names = ["add_ref", "unfreeze", "commit"]
    results: list[dict[str, Any]] = []
    for order in itertools.permutations(names):
        store, token = build_base()
        trace: list[str] = []
        for name in order:
            try:
                trace.append(_run_op(store, token, name))
            except StoreError as exc:
                trace.append(f"{name} 被拒绝: {exc}")
        followup = None
        if store.committed_plans:
            plan2 = create_plan(store, 70, tick=70)
            cert2 = commit(store, plan2.plan["plan_token"], 70, tick=70)
            followup = {
                "deleted": [d["node_id"] for d in cert2["deleted"]],
                "blocked": [b["node_id"] for b in cert2["blocked"]],
            }
        survivors = sorted(key_str(k) for k in store.instances)
        results.append({
            "order": list(order),
            "trace": trace,
            "followup_gen2": followup,
            "final_survivors": survivors,
            "final_hash": content_hash(survivors),
        })
    return results
