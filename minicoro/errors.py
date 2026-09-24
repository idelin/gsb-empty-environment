"""统一错误类型。所有源码位置均使用 UTF-8 字节区间。"""


class LangError(Exception):
    """语言层面的错误（词法/语法/运行时），可选携带 UTF-8 字节区间。"""

    def __init__(self, message, start=None, end=None):
        self.message = message
        self.start = start
        self.end = end
        if start is not None and end is not None:
            message = f"{message} (bytes {start}..{end})"
        super().__init__(message)


class ReentrancyError(LangError, RuntimeError):
    """协程运行中被重入（resume/close）时抛出。"""
