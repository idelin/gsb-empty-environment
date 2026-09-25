"""Dictionary-encoded batch merging utilities."""

from .dictionaries import (
    CollisionSafeMap,
    Column,
    DictBatch,
    EncodedChunk,
    EncodedColumn,
    LocalDictionary,
    TermSpec,
    decode_with_dictionary,
    split_batch_random,
)
from .errors import (
    ChunkCoverageError,
    DictionaryVersionError,
    InvalidDictionaryCodeError,
    MergeCancelled,
    StableTermConflictError,
    WithdrawnTermError,
)
from .merger import MergeJob, MergeResult
from .spilling import CancellationToken, MemoryBudget

__all__ = [
    "ChunkCoverageError",
    "CollisionSafeMap",
    "Column",
    "CancellationToken",
    "DictBatch",
    "DictionaryVersionError",
    "EncodedChunk",
    "EncodedColumn",
    "InvalidDictionaryCodeError",
    "LocalDictionary",
    "MemoryBudget",
    "MergeCancelled",
    "MergeJob",
    "MergeResult",
    "StableTermConflictError",
    "TermSpec",
    "WithdrawnTermError",
    "decode_with_dictionary",
    "split_batch_random",
]
