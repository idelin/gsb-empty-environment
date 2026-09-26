"""全扫描暴力参考实现：用于对照验证索引剪枝结果不漏词、不多词。"""

from __future__ import annotations

from .distance import Weights, weighted_distance
from .index import Match
from .tokenizer import normalize_query


def full_scan_search(
    vocab: list[str], query: str, weights: Weights, max_dist: float
) -> list[Match]:
    """阈值查询参考：距离 <= max_dist（含等号），按 (距离, 序号) 稳定排序。"""
    q = normalize_query(query)
    scored = [
        (weighted_distance(q, word, weights), seq, word)
        for seq, word in enumerate(vocab)
    ]
    scored.sort(key=lambda item: (item[0], item[1]))
    return [Match(word, dist) for dist, _, word in scored if dist <= max_dist]


def full_scan_topk(
    vocab: list[str], query: str, weights: Weights, k: int
) -> list[Match]:
    """TopK 参考：第 k 名距离上的等距候选完整保留，与索引语义一致。"""
    if k < 1:
        raise ValueError("k 必须 >= 1")
    q = normalize_query(query)
    scored = [
        (weighted_distance(q, word, weights), seq, word)
        for seq, word in enumerate(vocab)
    ]
    scored.sort(key=lambda item: (item[0], item[1]))
    if len(scored) <= k:
        cutoff = float("inf")
    else:
        cutoff = scored[k - 1][0]
    return [Match(word, dist) for dist, _, word in scored if dist <= cutoff]
