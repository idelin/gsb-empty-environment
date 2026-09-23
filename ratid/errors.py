"""错误类型：预算超限与超出支持域。"""


class RatIdError(Exception):
    """本模块内所有可预期错误的基类。"""


class BudgetExceeded(RatIdError):
    """展开后项数超过预算（多项式系数乘积项数也算在内）。"""

    def __init__(self, terms: int, budget: int, where: str = ""):
        self.terms = terms
        self.budget = budget
        self.where = where
        msg = f"项数预算超限：{terms} > {budget}"
        if where:
            msg += f"（{where}）"
        super().__init__(msg)


class Unsupported(RatIdError):
    """表达式超出本工具的支持域（非一元、非整数指数、非法语法等）。"""
