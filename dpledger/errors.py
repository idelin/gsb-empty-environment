"""账本异常类型定义。"""


class LedgerError(Exception):
    """账本操作的基础异常。"""


class ConflictError(LedgerError):
    """同一 query_id 提交了不同内容（内容哈希不一致）。"""


class InvalidStateError(LedgerError):
    """状态机不允许的迁移，例如取消已发布查询。"""


class BudgetExhaustedError(LedgerError):
    """预留会超出总预算。"""


class UnknownQueryError(LedgerError):
    """查询 ID 不存在。"""


class InvariantViolation(LedgerError):
    """独立校验状态机发现账本不变量被破坏。"""
