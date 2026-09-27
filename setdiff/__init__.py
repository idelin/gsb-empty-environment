"""增量集合差异证明（incremental set difference proof）。

两端各维护 键 -> (版本, 值) 映射及删除墓碑，通过分层哈希摘要定位差异。
摘要仅用于剪枝（概率模式）；精确模式提供显式逐条核验与可复核差异列表。
"""

from .hasher import ConstantHasher, Hasher, Sha256Hasher
from .model import Record
from .protocol import (
    DiffEntry,
    Peer,
    ReconciliationResult,
    RecordView,
    Stats,
    reconcile,
)
from .store import MergeReport, VersionedStore
from .tree import DigestTree

__all__ = [
    "ConstantHasher",
    "DiffEntry",
    "DigestTree",
    "Hasher",
    "MergeReport",
    "Peer",
    "Record",
    "RecordView",
    "ReconciliationResult",
    "Sha256Hasher",
    "Stats",
    "VersionedStore",
    "reconcile",
]

__version__ = "0.1.0"
