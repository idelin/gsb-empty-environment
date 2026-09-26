"""时间桶文档频率趋势索引。"""

from .index import TrendIndex, TrendRecord
from .tokenizer import tokenize

__all__ = ["TrendIndex", "TrendRecord", "tokenize"]
