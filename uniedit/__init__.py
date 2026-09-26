"""uniedit：按（加权）编辑距离检索 Unicode 词集。

- distance: 精确加权编辑距离 DP（码点级，支持相邻换位）
- check_metric: 校验成本模型在给定字母表上是否保证度量性质
- BKTree: 度量保证成立时的剪枝索引
- WordIndex: 安全索引——度量时走 BK 树，否则回退到带可证明下界的全扫描
"""
from .costs import CostModel
from .distance import distance, length_lower_bound
from .metric import MetricReport, check_metric
from .bktree import BKTree
from .tokenize import Token, tokenize
from .index import Hit, SearchResult, WordIndex

__all__ = [
    "CostModel",
    "distance",
    "length_lower_bound",
    "MetricReport",
    "check_metric",
    "BKTree",
    "Token",
    "tokenize",
    "Hit",
    "SearchResult",
    "WordIndex",
]
