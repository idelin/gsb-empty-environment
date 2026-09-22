"""异常层级：管线内所有错误都是 PipelineError 或其子类。"""

from __future__ import annotations


class PipelineError(Exception):
    """算子管线的基类异常。"""


class OperatorError(PipelineError):
    """执行某个算子时底层错误被包装，携带算子名与原始异常。"""

    def __init__(self, operator: str, cause: BaseException):
        self.operator = operator
        self.cause = cause
        super().__init__(f"operator {operator!r} failed: {type(cause).__name__}: {cause}")


class CancelledError(PipelineError):
    """管线被外部取消（与 asyncio.CancelledError 无关）。"""

    def __init__(self, message: str = "pipeline cancelled"):
        super().__init__(message)
