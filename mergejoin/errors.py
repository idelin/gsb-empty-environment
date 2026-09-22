"""merge join 的异常类型与取消令牌。"""

from __future__ import annotations

import threading


class MergeJoinError(Exception):
    """merge join 相关错误的基类。"""


class OrderViolationError(MergeJoinError):
    """输入流未按键的非递减序提供。"""


class JoinCancelled(MergeJoinError):
    """连接运算被取消令牌中断。"""


class CancellationToken:
    """协作式取消令牌，可从其他线程置位。"""

    def __init__(self) -> None:
        self._event = threading.Event()

    def cancel(self) -> None:
        self._event.set()

    @property
    def cancelled(self) -> bool:
        return self._event.is_set()

    def raise_if_cancelled(self) -> None:
        if self._event.is_set():
            raise JoinCancelled("merge join cancelled")
