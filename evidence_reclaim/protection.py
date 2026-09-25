"""最小不可回收闭包与阻塞链。

闭包（受保护集合）的最小固定点：
  根1: 未过自定义保留期的节点        (R-RETENTION-ROOT)
  根2: 带活动冻结标记的节点          (R-FREEZE-ROOT)
  闭包: 受保护节点沿“有效引用”边传播   (R-LIVE-REF-ROOT / R-FREEZE-INHERIT)
因此“过期但仍被活结论引用”的节点仍在闭包内（R-EXPIRED-LIVE-REF）。
冻结撤回后重新调用本计算即重新求闭包（R-FREEZE-REVOKABLE）。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Set, Tuple

from .facts import NodeKey, NodeVersion, Reference, Store
from .ids import canonical, stable_id
from .rules import RULES

CHAINSTEP_V1 = "chainstep.v1"

# 步骤原因码
ROOT_RETENTION = "root:unexpired"
ROOT_FREEZE = "root:freeze"
EDGE_LIVE_REF = "edge:live-reference"
EDGE_FROZEN_INHERIT = "edge:frozen-inheritance"

# 原因码 -> 规则 ID
REASON_RULE = {
    ROOT_RETENTION: "R-RETENTION-ROOT",
    ROOT_FREEZE: "R-FREEZE-ROOT",
    EDGE_LIVE_REF: "R-LIVE-REF-ROOT",
    EDGE_FROZEN_INHERIT: "R-FREEZE-INHERIT",
}


@dataclass(frozen=True)
class ChainStep:
    """阻塞链上的一步（事实 + 适用规则），带稳定 ID 与版本。"""

    step_id: str
    node: NodeKey
    reason: str
    rule_id: str
    rule_version: int
    via_ref: Optional[str]  # 经由的引用 ID；根步骤为 None
    detail: str
    schema_version: int = 1

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": CHAINSTEP_V1,
            "schema_version": self.schema_version,
            "step_id": self.step_id,
            "node": list(self.node),
            "reason": self.reason,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "via_ref": self.via_ref,
            "detail": self.detail,
        }

    def stable_id(self) -> str:
        return self.step_id


def _make_step(
    node: NodeKey,
    reason: str,
    via_ref: Optional[str],
    detail: str,
    index: int,
    lineage: str,
) -> ChainStep:
    rule = RULES[REASON_RULE[reason]]
    content = {
        "node": list(node),
        "reason": reason,
        "rule_id": rule.rule_id,
        "rule_version": rule.version,
        "via_ref": via_ref,
        "detail": detail,
        "index": index,
        "lineage": lineage,
    }
    return ChainStep(
        step_id=stable_id("step", content),
        node=node,
        reason=reason,
        rule_id=rule.rule_id,
        rule_version=rule.version,
        via_ref=via_ref,
        detail=detail,
    )


@dataclass
class ProtectionResult:
    """闭包计算结果。"""

    now: int
    protected: Set[NodeKey]
    roots_retention: Set[NodeKey]
    roots_freeze: Set[NodeKey]
    parent: Dict[NodeKey, Tuple[NodeKey, str, Optional[str], str]]
    root_of: Dict[NodeKey, Tuple[NodeKey, str, str]]
    depth: Dict[NodeKey, int]
    outgoing: Dict[NodeKey, List[Reference]]
    chain_index: Dict[NodeKey, int]  # 链中位置（根为 0），随首次标记确定
    generation: int

    def is_protected(self, key: NodeKey) -> bool:
        return key in self.protected

    def blocking_chain(self, target: NodeKey) -> Optional[List[ChainStep]]:
        """返回从根到 target 的引用阻塞链；不受保护则返回 None。"""

        if target not in self.protected:
            return None
        root, root_reason, root_detail = self.root_of[target]
        seq: List[Tuple[NodeKey, str, Optional[str], str, str]] = []
        cur = target
        while True:
            if cur == root:
                seq.append((cur, root_reason, None, root_detail, "root"))
                break
            parent, reason, via_ref, detail = self.parent[cur]
            seq.append((cur, reason, via_ref, detail, "edge"))
            cur = parent
        seq.reverse()

        labels = [f"{n[0][0]}#v{n[0][1]}" for n in seq]
        lineage = "->".join(labels)
        steps: List[ChainStep] = []
        for index, (node, reason, via_ref, detail, kind) in enumerate(seq):
            # 冻结谱系上的传播边标注为“冻结继承”
            if kind == "edge" and root_reason == ROOT_FREEZE:
                reason = EDGE_FROZEN_INHERIT
                detail = f"冻结沿有效引用 {via_ref} 继承自 {seq[index - 1][0][0]}#v{seq[index - 1][0][1]}"
            steps.append(_make_step(node, reason, via_ref, detail, index, lineage))
        return steps


def _sorted_outgoing(store: Store) -> Dict[NodeKey, List[Reference]]:
    outgoing: Dict[NodeKey, List[Reference]] = {}
    for ref in store.live_refs().values():
        outgoing.setdefault(ref.source, []).append(ref)
    for refs in outgoing.values():
        refs.sort(key=lambda r: (r.target, r.ref_id))
    return outgoing


def compute_closure(store: Store, now: int, generation: int) -> ProtectionResult:
    """以确定性 BFS 求最小不可回收闭包。

    冻结根优先于保留期根，保证“过期但显式冻结”的链头记为冻结。
    """

    outgoing = _sorted_outgoing(store)

    roots_retention: Set[NodeKey] = set()
    roots_freeze: Set[NodeKey] = set()
    node_seed_rank: Dict[NodeKey, int] = {}

    for key in sorted(store.nodes):
        node = store.nodes[key]
        if not node.is_expired(now):
            roots_retention.add(key)
            node_seed_rank[key] = min(node_seed_rank.get(key, 10**9), 1)

    for freeze_id in sorted(store.freezes):
        marker = store.freezes[freeze_id]
        if marker.active and marker.target in store.nodes:
            roots_freeze.add(marker.target)
            node_seed_rank[marker.target] = min(node_seed_rank.get(marker.target, 10**9), 0)

    seed_order = sorted(
        set(roots_retention) | set(roots_freeze),
        key=lambda k: (node_seed_rank.get(k, 9), k),
    )

    protected: Set[NodeKey] = set()
    parent: Dict[NodeKey, Tuple[NodeKey, str, Optional[str], str]] = {}
    root_of: Dict[NodeKey, Tuple[NodeKey, str, str]] = {}
    depth: Dict[NodeKey, int] = {}
    chain_index: Dict[NodeKey, int] = {}

    queue: List[NodeKey] = []
    for seed in seed_order:
        if seed in protected:
            continue
        protected.add(seed)
        depth[seed] = 0
        chain_index[seed] = 0
        node = store.nodes[seed]
        if seed in roots_freeze:
            reason = ROOT_FREEZE
            active_freeze_ids = sorted(
                fid
                for fid, m in store.freezes.items()
                if m.active and m.target == seed
            )
            detail = f"节点带活动冻结标记 {active_freeze_ids}，显式冻结"
        else:
            reason = ROOT_RETENTION
            detail = (
                f"当前时间 {now} 未超过自定义保留期 "
                f"(created_at={node.created_at}, retention={node.retention_seconds}, "
                f"expires_at={node.expires_at()})"
            )
        root_of[seed] = (seed, reason, detail)
        queue.append(seed)

    head = 0
    while head < len(queue):
        cur = queue[head]
        head += 1
        seed = root_of[cur][0]
        for ref in outgoing.get(cur, []):
            tgt = ref.target
            if tgt in protected:
                continue
            protected.add(tgt)
            parent[tgt] = (
                cur,
                EDGE_LIVE_REF,
                ref.ref_id,
                f"被存活节点 {cur[0]}#v{cur[1]} 的有效引用 {ref.ref_id} 指向",
            )
            root_of[tgt] = root_of[cur]
            depth[tgt] = depth[cur] + 1
            chain_index[tgt] = len(queue)  # 标记顺序（根之后的第 N 个被发现节点）
            queue.append(tgt)

    return ProtectionResult(
        now=now,
        protected=protected,
        roots_retention=roots_retention,
        roots_freeze=roots_freeze,
        parent=parent,
        root_of=root_of,
        depth=depth,
        outgoing=outgoing,
        chain_index=chain_index,
        generation=generation,
    )
