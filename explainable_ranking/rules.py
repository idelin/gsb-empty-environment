"""Versioned ranking rules: term weights, filters, cutoffs."""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Filter:
    """A deterministic document filter.

    Kinds:
      min_tokens      - drop docs with fewer than ``value`` tokens
      forbidden_term  - drop docs containing ``value``
      required_term   - drop docs not containing ``value``
    """

    kind: str
    value: object

    def matches(self, tokens: list[str]) -> bool:
        if self.kind == "min_tokens":
            return len(tokens) < int(self.value)
        if self.kind == "forbidden_term":
            return str(self.value) in tokens
        if self.kind == "required_term":
            return str(self.value) not in tokens
        raise ValueError(f"unknown filter kind: {self.kind!r}")

    def describe(self, tokens: list[str]) -> str:
        if self.kind == "min_tokens":
            return f"token_count={len(tokens)} < min_tokens={self.value}"
        if self.kind == "forbidden_term":
            return f"document contains forbidden term {self.value!r}"
        if self.kind == "required_term":
            return f"document misses required term {self.value!r}"
        raise ValueError(f"unknown filter kind: {self.kind!r}")


@dataclass(frozen=True)
class RuleSet:
    version: str
    weights: dict[str, float]
    filters: tuple[Filter, ...] = ()
    top_k: int = 10
    score_threshold: float = 0.0

    def __post_init__(self) -> None:
        for term, weight in self.weights.items():
            if weight < 0:
                raise ValueError(f"negative weight for term {term!r}")
        if self.top_k < 1:
            raise ValueError("top_k must be >= 1")

    def weight(self, term: str) -> float:
        return self.weights.get(term, 1.0)

    def first_matching_filter(self, tokens: list[str]) -> Filter | None:
        for filt in self.filters:
            if filt.matches(tokens):
                return filt
        return None


class RuleStore:
    def __init__(self) -> None:
        self._versions: dict[str, RuleSet] = {}

    def put(self, rules: RuleSet) -> None:
        self._versions[rules.version] = rules

    def get(self, version: str) -> RuleSet | None:
        return self._versions.get(version)
