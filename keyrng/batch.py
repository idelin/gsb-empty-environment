"""Batch transforms and chunked execution with stable per-sample streams.

Each sample's random stream is derived from its stable sample id:

    stream_key(sample) = fold_in(base_key, sample_id)

so results for a given sample id do not depend on how the batch is
chunked, shuffled, or scheduled.  This is what makes chunked data
loading and distributed or sharded execution reproducible.
"""

from __future__ import annotations

from typing import Any, Callable, Iterable

from .prng import PRNGKey, fold_in


def sample_key(base_key: PRNGKey, sample_id) -> PRNGKey:
    """The random stream owned by one sample, derived from its stable id."""
    return fold_in(base_key, sample_id)


def map_samples(
    base_key: PRNGKey,
    sample_ids: Iterable,
    fn: Callable[[PRNGKey, Any], Any],
) -> dict:
    """Apply ``fn(key, sample_id)`` to each sample; returns id -> value."""
    return {sid: fn(sample_key(base_key, sid), sid) for sid in sample_ids}


def map_samples_chunked(
    base_key: PRNGKey,
    sample_ids: Iterable,
    fn: Callable[[PRNGKey, Any], Any],
    chunk_size: int,
) -> dict:
    """Same as map_samples but executed in chunks.

    Chunking only affects scheduling: each sample still gets
    fold_in(base_key, sample_id), so results are identical for any
    chunk size.
    """
    if chunk_size < 1:
        raise ValueError("chunk_size must be >= 1")
    ids = list(sample_ids)
    out: dict = {}
    for start in range(0, len(ids), chunk_size):
        out.update(map_samples(base_key, ids[start : start + chunk_size], fn))
    return out
