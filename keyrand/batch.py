"""批处理变换：按稳定样本 ID 分配随机流，与分块方式无关。"""
from __future__ import annotations

from typing import Callable, Hashable, Iterable

from .prng import Key, fold_in


def sample_key(root: Key, sample_id: int) -> Key:
    """样本随机流 = fold_in(root, 稳定样本ID)，与执行顺序/分块无关。"""
    return fold_in(root, sample_id)


def batched_map(
    fn: Callable,
    root: Key,
    samples: Iterable[tuple[int, object]],
    chunk_size: int | None = None,
) -> dict[Hashable, object]:
    """对 (sample_id, value) 序列逐样本应用 fn(key, value)。

    每个样本的键只依赖其稳定 ID，因此改变 chunk_size 不改变任何样本
    的随机值。
    """
    items = list(samples)
    results: dict[Hashable, object] = {}
    step = chunk_size if chunk_size else len(items) or 1
    for start in range(0, len(items), step):
        for sample_id, value in items[start : start + step]:
            results[sample_id] = fn(sample_key(root, sample_id), value)
    return results
