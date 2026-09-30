"""Explainable, independently verifiable ranking (stdlib only)."""

from .corpus import Corpus, CorpusStore, Document
from .ranker import explain_miss, rank
from .rules import Filter, RuleSet, RuleStore
from .stats import StatsSnapshot, StatsStore
from .tokenizer import tokenize
from .verifier import Verdict, verify

__all__ = [
    "Corpus",
    "CorpusStore",
    "Document",
    "Filter",
    "RuleSet",
    "RuleStore",
    "StatsSnapshot",
    "StatsStore",
    "Verdict",
    "explain_miss",
    "rank",
    "tokenize",
    "verify",
]
