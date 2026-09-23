"""异常类型：词法、语法、结构（打印）、求值、语法表配置。"""


class ExprError(Exception):
    """所有表达式库异常的基类。"""


class LexError(ExprError):
    """词法分析失败（无法识别的字符/字面量）。"""


class ParseError(ExprError):
    """语法分析失败（不符合文法）。"""


class GrammarError(ExprError):
    """运算优先级表有歧义或不合法。"""


class StructureError(ExprError):
    """待打印的语法树不满足打印器前置条件。"""


class EvalError(ExprError):
    """精确求值失败（未绑定符号、函数未定义）。"""


class DomainError(EvalError):
    """运算被施加在其适用域之外（除零、非整数次幂等）。"""
