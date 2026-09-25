"""交错枚举：引用新增、解冻、回收提交三类操作的全排列试演。

对同一基线状态与同一试算计划，枚举三种操作的全部 3! 种交错顺序，
在各自的引擎副本上执行并记录：每步是否被接纳、提交是否成功、
最终状态根与存活证明。用于验证：
  - 新引用必须先于提交才能阻止删除（R-CAS-NEW-REF）；
  - 提交后新引用因悬空依赖被拒绝（R-NO-DANGLING）；
  - 解冻后需重新计算闭包（R-FREEZE-REVOKABLE）。
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional

from .engine import ReclaimEngine, ReclaimPlan
from .events import EventError

OP_NEW_REF = "new-reference"
OP_UNFREEZE = "unfreeze"
OP_COMMIT = "commit"


@dataclass
class OpOutcome:
    op: str
    admitted: bool
    detail: str

    def to_dict(self) -> Dict[str, Any]:
        return {"op": self.op, "admitted": self.admitted, "detail": self.detail}


@dataclass
class InterleavingScenario:
    """一次交错试演的完整记录。"""

    order: List[str]
    outcomes: List[OpOutcome] = field(default_factory=list)
    commit_status: str = "not-attempted"
    commit_reasons: List[str] = field(default_factory=list)
    cert_id: Optional[str] = None
    final_state_root: str = ""
    liveness_ok: bool = True
    remaining_nodes: List[str] = field(default_factory=list)
    recyclable_after: List[str] = field(default_factory=list)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "order": list(self.order),
            "outcomes": [o.to_dict() for o in self.outcomes],
            "commit_status": self.commit_status,
            "commit_reasons": list(self.commit_reasons),
            "cert_id": self.cert_id,
            "final_state_root": self.final_state_root,
            "liveness_ok": self.liveness_ok,
            "remaining_nodes": list(self.remaining_nodes),
            "recyclable_after": list(self.recyclable_after),
        }


def enumerate_interleavings(
    engine: ReclaimEngine,
    plan: ReclaimPlan,
    now: int,
    new_ref: Dict[str, Any],
    freeze_id: str,
) -> List[InterleavingScenario]:
    """枚举 [新引用, 解冻, 提交] 的全部交错顺序并逐一试演。

    engine 与 plan 不被修改；每种顺序在独立副本上执行。
    """

    ops = [OP_NEW_REF, OP_UNFREEZE, OP_COMMIT]
    scenarios: List[InterleavingScenario] = []

    for order in itertools.permutations(ops):
        replica = engine.clone()
        scenario = InterleavingScenario(order=list(order))

        for op in order:
            if op == OP_NEW_REF:
                try:
                    replica.add_reference(
                        new_ref["ref_id"],
                        tuple(new_ref["source"]),
                        tuple(new_ref["target"]),
                        new_ref["created_at"],
                    )
                    scenario.outcomes.append(OpOutcome(op, True, "引用已接纳"))
                except EventError as exc:
                    scenario.outcomes.append(OpOutcome(op, False, f"引用被拒绝: {exc}"))

            elif op == OP_UNFREEZE:
                try:
                    replica.revoke_freeze(freeze_id)
                    scenario.outcomes.append(OpOutcome(op, True, "冻结已撤回，闭包将重算"))
                except EventError as exc:
                    scenario.outcomes.append(OpOutcome(op, False, f"解冻被拒绝: {exc}"))

            elif op == OP_COMMIT:
                cert, rejection = replica.commit(plan, now=now)
                if cert is not None:
                    scenario.commit_status = "committed"
                    scenario.cert_id = cert.cert_id
                    scenario.liveness_ok = cert.liveness.ok
                    scenario.outcomes.append(
                        OpOutcome(op, True, f"提交成功，证书 {cert.cert_id}")
                    )
                else:
                    scenario.commit_status = "rejected"
                    scenario.commit_reasons = list(rejection.reasons)
                    scenario.outcomes.append(
                        OpOutcome(op, False, "提交被拒绝: " + "；".join(rejection.reasons))
                    )

        store = replica.log.store
        scenario.final_state_root = store.state_root()
        dangling = [
            rid for rid, ref in store.refs.items()
            if ref.source not in store.nodes or ref.target not in store.nodes
        ]
        scenario.liveness_ok = scenario.liveness_ok and not dangling
        scenario.remaining_nodes = sorted(f"{k[0]}#v{k[1]}" for k in store.nodes)
        followup = replica.plan(now)
        scenario.recyclable_after = sorted(f"{k[0]}#v{k[1]}" for k in followup.candidates)
        scenarios.append(scenario)

    return scenarios
