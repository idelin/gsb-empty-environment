"""minisearch：本地合成 UTF-8 文本上的布尔/短语/模糊查询，
带成本估算与共享操作预算。仅用标准库。"""

from .budget import Budget, BudgetExhausted
from .engine import Engine, SearchResult, Stats
from .index import Index
from .queries import And, Fuzzy, Not, Or, Phrase, Term
from .tokenizer import tokenize

__all__ = [
    "And", "Budget", "BudgetExhausted", "Engine", "Fuzzy", "Index", "Not",
    "Or", "Phrase", "SearchResult", "Stats", "Term", "tokenize",
]
