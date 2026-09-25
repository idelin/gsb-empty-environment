"""masked_matmul 的异常类型。"""


class MaskedMatmulError(Exception):
    """所有库异常的基类。"""


class BoundsError(MaskedMatmulError, IndexError):
    """索引超出显式声明的尺寸。"""


class RetractionError(MaskedMatmulError):
    """撤回与已有插入不匹配(计数为负或权重未抵消)。"""


class SpillError(MaskedMatmulError):
    """热点行溢写中间贡献时发生 I/O 失败。"""


class UnknownVersionError(MaskedMatmulError):
    """查询绑定的版本向量从未被提交发布。"""


class TransactionStateError(MaskedMatmulError):
    """事务状态非法(重复提交、并发事务等)。"""
