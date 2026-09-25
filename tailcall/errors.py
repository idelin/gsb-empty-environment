"""语言层面的错误类型。"""


class LangError(Exception):
    """语法错误或运行时错误。

    span 是源码的 UTF-8 字节区间 [start, end)；
    traceback_lines 是未捕获时附上的逻辑调用轨迹。
    """

    def __init__(self, message, span=None):
        super().__init__(message)
        self.message = message
        self.span = span
        self.traceback_lines = []

    def __str__(self):
        text = self.message
        if self.span is not None:
            text += "（字节区间 {}..{}）".format(self.span[0], self.span[1])
        if self.traceback_lines:
            text += "\n" + "\n".join(self.traceback_lines)
        return text
