"""引擎专用异常类型。"""


class ParseError(Exception):
    """正则表达式语法错误，携带源码位置。"""

    def __init__(self, message, pos):
        super().__init__(f"{message}（源码位置 {pos}）")
        self.pos = pos


class CompileError(Exception):
    """编译期错误（如字符超出显式字母表）。"""


class BudgetExceeded(Exception):
    """确定化状态数超出预算；调用方应标记“未完成”而非返回错误结果。"""
