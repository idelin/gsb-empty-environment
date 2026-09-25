"""分片拼接：校验边界证据、重放证明、良基接地、检测无根假设环、去重贡献。"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import FactRef, Proof, Shard
from .replay import ReplayError, replay

CONFLICTING_SHARD = "CONFLICTING_SHARD_VERSION"
REPLAY_FAILURE = "REPLAY_FAILURE"
UNSATISFIED_ASSUMPTION = "UNSATISFIED_ASSUMPTION"
BOUNDARY_DIGEST_MISMATCH = "BOUNDARY_DIGEST_MISMATCH"
UNGROUNDED_PROVIDER = "UNGROUNDED_PROVIDER"
ASSUMPTION_CYCLE_NO_ROOT = "ASSUMPTION_CYCLE_NO_ROOT"


@dataclass(frozen=True)
class Rejection:
    shard_id: str
    proof_id: str | None
    reason: str
    detail: str


@dataclass
class StitchReport:
    grounded: dict[FactRef, str]            # 已接地的结论 -> 证据摘要
    contributions: dict[str, int]           # 每个分片计入的唯一接地证明数
    total_unique_proofs: int                # 全局去重后的接地证明数
    duplicates_ignored: list[tuple[str, int]]
    rejections: list[Rejection]
    cycles: list[tuple[str, ...]]           # 检测到的分片间假设环


def _find_cycles(edges: dict[str, set[str]]) -> list[tuple[str, ...]]:
    """Tarjan 强连通分量；报告大小 >1 或带自环的分量。"""
    index_of: dict[str, int] = {}
    lowlink: dict[str, int] = {}
    on_stack: set[str] = set()
    stack: list[str] = []
    counter = [0]
    cycles: list[tuple[str, ...]] = []

    def strongconnect(node: str) -> None:
        index_of[node] = lowlink[node] = counter[0]
        counter[0] += 1
        stack.append(node)
        on_stack.add(node)
        for nxt in sorted(edges.get(node, ())):
            if nxt not in index_of:
                strongconnect(nxt)
                lowlink[node] = min(lowlink[node], lowlink[nxt])
            elif nxt in on_stack:
                lowlink[node] = min(lowlink[node], index_of[nxt])
        if lowlink[node] == index_of[node]:
            scc: list[str] = []
            while True:
                member = stack.pop()
                on_stack.discard(member)
                scc.append(member)
                if member == node:
                    break
            if len(scc) > 1 or scc[0] in edges.get(scc[0], ()):
                cycles.append(tuple(sorted(scc)))

    for node in sorted(edges):
        if node not in index_of:
            strongconnect(node)
    return cycles


class Stitcher:
    """收集分片并拼接：假设必须由另一分片的有效证据满足。"""

    def __init__(self) -> None:
        self._shards: dict[tuple[str, int], Shard] = {}
        self._rejections: list[Rejection] = []
        self._duplicates: list[tuple[str, int]] = []

    def submit(self, shard: Shard) -> None:
        key = (shard.shard_id, shard.version)
        existing = self._shards.get(key)
        if existing is None:
            self._shards[key] = shard
        elif existing.content_digest() == shard.content_digest():
            self._duplicates.append(key)  # 重复提交：忽略，不重复计贡献
        else:
            self._rejections.append(Rejection(
                shard.shard_id, None, CONFLICTING_SHARD,
                f"分片 {shard.shard_id}@{shard.version} 已存在且内容摘要不同",
            ))

    def stitch(self) -> StitchReport:
        rejections = list(self._rejections)
        valid: list[tuple[str, Proof, str]] = []
        index: dict[FactRef, list[tuple[str, Proof, str]]] = {}
        for key in sorted(self._shards):
            shard = self._shards[key]
            for proof in shard.proofs:
                try:
                    replay(proof, shard.rules)
                except ReplayError as exc:
                    rejections.append(Rejection(shard.shard_id, proof.proof_id, REPLAY_FAILURE, str(exc)))
                    continue
                digest = proof.digest()
                valid.append((shard.shard_id, proof, digest))
                index.setdefault(proof.conclusion, []).append((shard.shard_id, proof, digest))

        # 良基接地：无假设的证明是根；假设须由已接地且摘要匹配的证据满足。
        grounded_digests: set[str] = set()
        changed = True
        while changed:
            changed = False
            for _sid, proof, digest in valid:
                if digest in grounded_digests:
                    continue
                if all(
                    any(pd == a.expected_digest and pd in grounded_digests
                        for _, _, pd in index.get(a.ref, ()))
                    for a in proof.assumptions
                ):
                    grounded_digests.add(digest)
                    changed = True

        # 对未接地的证明分类拒绝原因，并收集分片间依赖边用于环检测。
        edges: dict[str, set[str]] = {}
        for sid, proof, digest in valid:
            if digest in grounded_digests:
                continue
            for assumption in proof.assumptions:
                providers = index.get(assumption.ref, [])
                if not providers:
                    rejections.append(Rejection(
                        sid, proof.proof_id, UNSATISFIED_ASSUMPTION,
                        f"假设 {assumption.assumption_id} 引用 {assumption.ref}，无任何分片提供证据",
                    ))
                elif not any(pd == assumption.expected_digest for _, _, pd in providers):
                    rejections.append(Rejection(
                        sid, proof.proof_id, BOUNDARY_DIGEST_MISMATCH,
                        f"假设 {assumption.assumption_id} 期望摘要 "
                        f"{assumption.expected_digest[:12]}… 与提供方证据摘要不匹配",
                    ))
                else:
                    for psid, _, pd in providers:
                        if pd == assumption.expected_digest:
                            edges.setdefault(sid, set()).add(psid)
        cycles = _find_cycles(edges)
        cyclic_shards = {s for cycle in cycles for s in cycle}
        for sid, proof, digest in valid:
            if digest in grounded_digests:
                continue
            all_matched = all(
                any(pd == a.expected_digest for _, _, pd in index.get(a.ref, ()))
                for a in proof.assumptions
            )
            if not all_matched:
                continue  # 已按 UNSATISFIED / DIGEST_MISMATCH 报告
            if sid in cyclic_shards:
                rejections.append(Rejection(
                    sid, proof.proof_id, ASSUMPTION_CYCLE_NO_ROOT,
                    "互相假设构成无根环，不能凭空成立",
                ))
            else:
                rejections.append(Rejection(
                    sid, proof.proof_id, UNGROUNDED_PROVIDER,
                    "摘要匹配的提供方证据自身未接地（上游未被满足）",
                ))

        grounded: dict[FactRef, str] = {}
        contributions: dict[str, int] = {}
        seen: set[str] = set()
        for sid, proof, digest in valid:
            if digest not in grounded_digests:
                continue
            grounded.setdefault(proof.conclusion, digest)
            if digest not in seen:  # 重复证明不重复计贡献
                seen.add(digest)
                contributions[sid] = contributions.get(sid, 0) + 1
        return StitchReport(
            grounded=grounded,
            contributions=contributions,
            total_unique_proofs=len(seen),
            duplicates_ignored=list(self._duplicates),
            rejections=rejections,
            cycles=cycles,
        )
