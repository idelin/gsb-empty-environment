"""txguard: 更新前前值校验参考模型（本地夹具驱动，无消息平台依赖）。"""

from .models import (
    MISSING,
    ChangeOp,
    ConflictKind,
    FieldConflict,
    OpConflict,
    Transaction,
    TxResult,
    TxStatus,
)
from .engine import Engine
from .loader import load_fixture

__all__ = [
    "MISSING",
    "ChangeOp",
    "ConflictKind",
    "Engine",
    "FieldConflict",
    "OpConflict",
    "Transaction",
    "TxResult",
    "TxStatus",
    "load_fixture",
]
