"""独立重算校验：从原始记录重新统计，核对增量计数、null 处理与分片合并。"""

from __future__ import annotations

from collections import Counter, defaultdict

from .core import Dataset, Record


def recount(records: list[Record]) -> dict[tuple, Counter]:
    """独立路径：直接从记录列表重建等价类计数（含 None 键）。"""
    counts: dict[tuple, Counter] = defaultdict(Counter)
    for record in records:
        counts[record.qid][record.sensitive] += 1
    return {qid: Counter(c) for qid, c in counts.items()}


def verify_incremental(dataset: Dataset) -> bool:
    """增量维护（含删除）后的计数与全量重算一致则 True。"""
    return dataset.class_counts() == recount(dataset.records)


def merge_shards(shards: list[list[Record]]) -> dict[tuple, Counter]:
    """分片合并：各分片独立计数后按类相加。"""
    merged: dict[tuple, Counter] = defaultdict(Counter)
    for shard in shards:
        for qid, counter in recount(shard).items():
            merged[qid].update(counter)
    return {qid: Counter(c) for qid, c in merged.items()}


def verify_shard_merge(shards: list[list[Record]]) -> bool:
    """分片合并结果与拼接后直接重算一致则 True。"""
    flat = [record for shard in shards for record in shard]
    return merge_shards(shards) == recount(flat)
