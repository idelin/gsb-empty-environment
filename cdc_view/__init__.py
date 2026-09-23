"""后台重建物化视图：固定快照 + 增量缓存追平 + 原子切换。"""

from .events import ChangeEvent, load_events, load_snapshot
from .manager import RebuildError, ViewManager
from .store import BaseStore
from .view import FrozenViewError, MaterializedView

__all__ = [
    "BaseStore",
    "ChangeEvent",
    "FrozenViewError",
    "MaterializedView",
    "RebuildError",
    "ViewManager",
    "load_events",
    "load_snapshot",
]
