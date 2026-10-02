"""显式失败类型：任何不可估/无效情形都抛出具体异常，绝不静默继续。"""


class BootCIError(Exception):
    """bootci 所有异常的基类。"""


class InvalidResampleError(BootCIError):
    """重采样统计量无效（抛异常、返回 None/NaN/Inf 或非数值）。

    默认策略为显式失败：绝不在调用方不知情时丢弃无效重采样，
    以免静默改变名义置信度。
    """


class BCANotEstimableError(BootCIError):
    """BCa 区间不可估（如偏差修正 z0 为无穷、修正分位数越界）。"""


class AccelerationNotEstimableError(BCANotEstimableError):
    """jackknife 退化（留一值全相同或不足），加速度 a 不可估。"""


class EnumerationTooLargeError(BootCIError):
    """精确枚举的重采样空间超过显式上限。"""
