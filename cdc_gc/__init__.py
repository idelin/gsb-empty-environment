"""CDC 墓碑回收模拟器：本地夹具驱动，无消息平台依赖。"""
from .events import ChangeEvent, Op, load_fixture
from .gc import GarbageCollector, ReclaimDecision
from .group import ConsumerGroup, MembershipError
from .store import ApplyResult, MaterializedStore, Tombstone

__all__ = [
    "ApplyResult",
    "ChangeEvent",
    "ConsumerGroup",
    "GarbageCollector",
    "MaterializedStore",
    "MembershipError",
    "Op",
    "ReclaimDecision",
    "Tombstone",
    "load_fixture",
]
