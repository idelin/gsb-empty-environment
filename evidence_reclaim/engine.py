"""回收引擎：试算（dry-run）、提交验证、删除证书、存活证明与重放。

- 试算只产生计划，不修改任何状态（R-GENERATION 标记代际递增）。
- 提交前重新计算闭包并做版本校验（R-VERSION-CHECK）；
  计划后出现的新引用若覆盖候选，提交必须被拒绝（R-CAS-NEW-REF）。
- 提交产生可重放删除证书与存活证明（R-CERT-REPLAY / R-NO-DANGLING）。
"""

from __future__ import annotations

import copy
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Set, Tuple

from .events import EventLog, RECLAIM_COMMIT
from .facts import NodeKey, Store
from .ids import digest, stable_id
from .protection import ChainStep, ProtectionResult, compute_closure
from .rules import rules_digest

PLAN_V1 = "reclaim-plan.v1"
CERT_V1 = "reclaim-cert.v1"
PROOF_V1 = "liveness-proof.v1"
REJECTION_V1 = "commit-rejection.v1"


@dataclass
class ReclaimPlan:
    """试算产物：可回收集合 + 阻塞报告。绝不修改仓库状态。"""

    plan_id: str
    generation: int
    base_version: int
    now: int
    protected: List[NodeKey]
    candidates: List[NodeKey]
    blocked: List[Dict[str, Any]]  # 过期但不可回收节点的阻塞链报告
    plan_digest: str

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": PLAN_V1,
            "plan_id": self.plan_id,
            "generation": self.generation,
            "base_version": self.base_version,
            "now": self.now,
            "protected": [list(k) for k in self.protected],
            "candidates": [list(k) for k in self.candidates],
            "blocked": self.blocked,
            "plan_digest": self.plan_digest,
        }


@dataclass
class CommitRejection:
    """提交被拒绝：携带原因、阻塞链与新一代际提示。"""

    rejection_id: str
    plan_id: str
    reasons: List[str]
    blocked_chains: Dict[str, List[Dict[str, Any]]]
    new_generation_hint: int
    schema: str = REJECTION_V1

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": self.schema,
            "rejection_id": self.rejection_id,
            "plan_id": self.plan_id,
            "reasons": self.reasons,
            "blocked_chains": self.blocked_chains,
            "new_generation_hint": self.new_generation_hint,
        }


@dataclass
class LivenessProof:
    """存活证明：提交后无悬空依赖。"""

    post_version: int
    post_state_root: str
    dangling_refs: List[str]
    ok: bool
    schema: str = PROOF_V1

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": self.schema,
            "post_version": self.post_version,
            "post_state_root": self.post_state_root,
            "dangling_refs": self.dangling_refs,
            "ok": self.ok,
        }


@dataclass
class DeletionCertificate:
    """可重放删除证书。"""

    cert_id: str
    plan_id: str
    generation: int
    base_version: int
    commit_version: int
    now: int
    deleted_nodes: List[NodeKey]
    deleted_refs: List[str]
    pre_state_root: str
    post_state_root: str
    rules_digest: str
    liveness: LivenessProof
    schema: str = CERT_V1

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": self.schema,
            "cert_id": self.cert_id,
            "plan_id": self.plan_id,
            "generation": self.generation,
            "base_version": self.base_version,
            "commit_version": self.commit_version,
            "now": self.now,
            "deleted_nodes": [list(k) for k in self.deleted_nodes],
            "deleted_refs": list(self.deleted_refs),
            "pre_state_root": self.pre_state_root,
            "post_state_root": self.post_state_root,
            "rules_digest": self.rules_digest,
            "liveness": self.liveness.to_dict(),
        }


def _key_str(key: NodeKey) -> str:
    return f"{key[0]}#v{key[1]}"


def _plan_digest_payload(
    generation: int,
    base_version: int,
    now: int,
    protected: List[NodeKey],
    candidates: List[NodeKey],
    blocked: List[Dict[str, Any]],
) -> Dict[str, Any]:
    return {
        "schema": PLAN_V1,
        "generation": generation,
        "base_version": base_version,
        "now": now,
        "protected": [list(k) for k in protected],
        "candidates": [list(k) for k in candidates],
        "blocked": blocked,
    }


class ReclaimEngine:
    """围绕事件日志的回收引擎。"""

    def __init__(self, log: Optional[EventLog] = None) -> None:
        self.log = log if log is not None else EventLog()
        self._mark_generation = 0

    # ---- 事实写入 ----

    def add_node(self, evidence_id: str, version: int, created_at: int,
                 retention_seconds: Optional[int], payload: str):
        return self.log.append("node.add", {
            "evidence_id": evidence_id,
            "version": version,
            "created_at": created_at,
            "retention_seconds": retention_seconds,
            "payload": payload,
        })

    def add_reference(self, ref_id: str, source: NodeKey, target: NodeKey, created_at: int):
        return self.log.append("ref.add", {
            "ref_id": ref_id,
            "source": list(source),
            "target": list(target),
            "created_at": created_at,
        })

    def revoke_reference(self, ref_id: str):
        return self.log.append("ref.revoke", {"ref_id": ref_id})

    def add_freeze(self, freeze_id: str, target: NodeKey, created_at: int):
        return self.log.append("freeze.add", {
            "freeze_id": freeze_id,
            "target": list(target),
            "created_at": created_at,
        })

    def revoke_freeze(self, freeze_id: str):
        return self.log.append("freeze.revoke", {"freeze_id": freeze_id})

    # ---- 闭包与阻塞链 ----

    def protection(self, now: int) -> ProtectionResult:
        return compute_closure(self.log.store, now, self._mark_generation + 1)

    def blocking_chain(self, target: NodeKey, now: int) -> Optional[List[ChainStep]]:
        return self.protection(now).blocking_chain(target)

    # ---- 试算（dry-run，不修改状态） ----

    def plan(self, now: int) -> ReclaimPlan:
        self._mark_generation += 1
        generation = self._mark_generation
        store = self.log.store
        result = compute_closure(store, now, generation)

        protected = sorted(result.protected)
        candidates = sorted(k for k in store.nodes if k not in result.protected)

        blocked: List[Dict[str, Any]] = []
        for key in protected:
            node = store.nodes[key]
            if node.is_expired(now):
                chain = result.blocking_chain(key)
                blocked.append({
                    "node": list(key),
                    "expires_at": node.expires_at(),
                    "reason": "expired-but-protected",
                    "chain": [s.to_dict() for s in chain] if chain else [],
                })

        base_version = self.log.version
        payload = _plan_digest_payload(generation, base_version, now, protected, candidates, blocked)
        plan_id = stable_id("plan", payload)
        return ReclaimPlan(
            plan_id=plan_id,
            generation=generation,
            base_version=base_version,
            now=now,
            protected=protected,
            candidates=candidates,
            blocked=blocked,
            plan_digest=digest(payload),
        )

    # ---- 提交验证 ----

    def _validate_plan(self, plan: ReclaimPlan, now: int) -> Optional[CommitRejection]:
        """结构校验 + 语义重算；通过返回 None，否则返回拒绝单。"""

        reasons: List[str] = []
        blocked_chains: Dict[str, List[Dict[str, Any]]] = {}

        # 1) 计划完整性（防篡改）
        payload = _plan_digest_payload(
            plan.generation, plan.base_version, plan.now,
            plan.protected, plan.candidates, plan.blocked,
        )
        if digest(payload) != plan.plan_digest:
            reasons.append("计划摘要校验失败：计划内容被篡改")

        # 2) 版本校验：存储版本必须等于计划基线
        if self.log.version != plan.base_version:
            reasons.append(
                f"存储版本已变化：计划基线 v{plan.base_version}，当前 v{self.log.version}"
            )

        # 3) 语义重算：候选在当前闭包下必须仍然可回收
        store = self.log.store
        result = compute_closure(store, now, plan.generation)
        for key in plan.candidates:
            if key not in store.nodes:
                reasons.append(f"候选节点已不存在: {_key_str(key)}")
                continue
            if key in result.protected:
                chain = result.blocking_chain(key)
                chain_dicts = [s.to_dict() for s in chain] if chain else []
                blocked_chains[_key_str(key)] = chain_dicts
                # 找出计划基线之后新增的、覆盖该候选的有效引用
                new_refs = sorted(
                    rid for rid, ref in store.live_refs().items()
                    if ref.target == key and self._ref_event_seq(rid) > plan.base_version
                )
                suffix = f"；计划后新增引用: {new_refs}" if new_refs else ""
                reasons.append(f"候选 {_key_str(key)} 已被新引用/冻结重新保护{suffix}")

        if not reasons:
            return None
        content = {
            "plan_id": plan.plan_id,
            "reasons": reasons,
            "blocked_chains": blocked_chains,
            "base_version": plan.base_version,
            "current_version": self.log.version,
        }
        return CommitRejection(
            rejection_id=stable_id("reject", content),
            plan_id=plan.plan_id,
            reasons=reasons,
            blocked_chains=blocked_chains,
            new_generation_hint=self._mark_generation + 1,
        )

    def _ref_event_seq(self, ref_id: str) -> int:
        for event in self.log.events:
            if event.kind == "ref.add" and event.body.get("ref_id") == ref_id:
                return event.seq
        return 0

    # ---- 提交 ----

    def commit(self, plan: ReclaimPlan, now: Optional[int] = None) -> Tuple[Optional[DeletionCertificate], Optional[CommitRejection]]:
        """验证并应用回收计划。成功返回 (证书, None)，失败返回 (None, 拒绝单)。"""

        commit_now = plan.now if now is None else now
        rejection = self._validate_plan(plan, commit_now)
        if rejection is not None:
            return None, rejection

        store = self.log.store
        candidates: Set[NodeKey] = set(plan.candidates)

        # 待删除引用：源或目标在候选集中的全部引用（含已撤销的墓碑）
        deleted_refs = sorted(
            rid for rid, ref in store.refs.items()
            if ref.source in candidates or ref.target in candidates
        )
        deleted_nodes = sorted(candidates)

        pre_root = store.state_root()
        cert_body = {
            "schema": CERT_V1,
            "plan_id": plan.plan_id,
            "generation": plan.generation,
            "base_version": plan.base_version,
            "now": commit_now,
            "deleted_nodes": [list(k) for k in deleted_nodes],
            "deleted_refs": deleted_refs,
            "pre_state_root": pre_root,
            "rules_digest": rules_digest(),
        }
        cert_id = stable_id("cert", cert_body)
        event = self.log.append(RECLAIM_COMMIT, {
            "schema": CERT_V1,
            "cert_id": cert_id,
            "plan_id": plan.plan_id,
            "generation": plan.generation,
            "deleted_nodes": [list(k) for k in deleted_nodes],
            "deleted_refs": deleted_refs,
        })

        # 存活证明：提交后不得存在悬空引用
        post_store = self.log.store
        dangling = sorted(
            rid for rid, ref in post_store.refs.items()
            if ref.source not in post_store.nodes or ref.target not in post_store.nodes
        )
        liveness = LivenessProof(
            post_version=self.log.version,
            post_state_root=post_store.state_root(),
            dangling_refs=dangling,
            ok=not dangling,
        )

        cert = DeletionCertificate(
            cert_id=cert_id,
            plan_id=plan.plan_id,
            generation=plan.generation,
            base_version=plan.base_version,
            commit_version=event.seq,
            now=commit_now,
            deleted_nodes=deleted_nodes,
            deleted_refs=deleted_refs,
            pre_state_root=pre_root,
            post_state_root=post_store.state_root(),
            rules_digest=rules_digest(),
            liveness=liveness,
        )
        return cert, None

    # ---- 重放 ----

    def replay(self) -> "ReclaimEngine":
        """从事件日志完整重放，验证状态可重建。"""

        rebuilt = ReclaimEngine(EventLog.from_records(self.log.event_records()))
        if rebuilt.log.store.state_root() != self.log.store.state_root():
            raise ValueError("重放状态根与当前状态不一致")
        return rebuilt

    def clone(self) -> "ReclaimEngine":
        new = ReclaimEngine(self.log.clone())
        new._mark_generation = self._mark_generation
        return new


def replay_certificate(engine: ReclaimEngine, cert: DeletionCertificate) -> List[str]:
    """独立重放验证删除证书，返回问题列表（空列表表示通过）。

    校验：证书摘要、删除集与候选一致、删除引用覆盖、前后状态根、
    存活证明无悬空依赖、规则摘要一致。
    """

    problems: List[str] = []
    cert_body = {
        "schema": CERT_V1,
        "plan_id": cert.plan_id,
        "generation": cert.generation,
        "base_version": cert.base_version,
        "now": cert.now,
        "deleted_nodes": [list(k) for k in cert.deleted_nodes],
        "deleted_refs": list(cert.deleted_refs),
        "pre_state_root": cert.pre_state_root,
        "rules_digest": cert.rules_digest,
    }
    if stable_id("cert", cert_body) != cert.cert_id:
        problems.append("证书稳定 ID 与内容不符")

    record = engine.log.store.reclaim_commits.get(cert.cert_id)
    if record is None:
        problems.append("仓库中不存在该证书的提交记录")
    else:
        if record["generation"] != cert.generation:
            problems.append("提交记录代际与证书不一致")
        if sorted(tuple(k) for k in record["deleted_nodes"]) != sorted(cert.deleted_nodes):
            problems.append("提交记录删除集与证书不一致")

    for key in cert.deleted_nodes:
        if key in engine.log.store.nodes:
            problems.append(f"证书声明已删除的节点仍存在: {_key_str(key)}")
    for rid in cert.deleted_refs:
        if rid in engine.log.store.refs:
            problems.append(f"证书声明已删除的引用仍存在: {rid}")

    if engine.log.store.state_root() != cert.post_state_root:
        problems.append("当前状态根与证书 post_state_root 不一致")

    dangling = sorted(
        rid for rid, ref in engine.log.store.refs.items()
        if ref.source not in engine.log.store.nodes or ref.target not in engine.log.store.nodes
    )
    if dangling:
        problems.append(f"存活证明失败：存在悬空引用 {dangling}")
    if not cert.liveness.ok:
        problems.append("证书存活证明标记为失败")
    if cert.liveness.post_state_root != cert.post_state_root:
        problems.append("证书存活证明状态根与 post_state_root 不一致")
    if cert.rules_digest != rules_digest():
        problems.append("规则摘要与当前规则注册表不一致")

    return problems
