"""Permission-scoped explanation cache.

Each clearance level gets an isolated store: an entry cached while
viewing at one clearance is never served to - or enumerable by - another
clearance, even when the underlying facts, rules and query are
identical.  This prevents a high-clearance explanation (which may embed
more structure) from leaking to a lower-clearance caller through a
shared cache.
"""
from __future__ import annotations


class ExplanationCache:
    def __init__(self):
        self._stores: dict[int, dict[str, object]] = {}

    @staticmethod
    def _key(facts_digest: str, rules_digest: str, query_key: str) -> str:
        return f"{facts_digest}|{rules_digest}|{query_key}"

    def get(self, clearance: int, facts_digest: str, rules_digest: str,
            query_key: str):
        store = self._stores.get(clearance)
        if store is None:
            return None
        return store.get(self._key(facts_digest, rules_digest, query_key))

    def put(self, clearance: int, facts_digest: str, rules_digest: str,
            query_key: str, explanation) -> None:
        store = self._stores.setdefault(clearance, {})
        store[self._key(facts_digest, rules_digest, query_key)] = explanation

    def size(self, clearance: int) -> int:
        return len(self._stores.get(clearance, {}))

    def clearances(self) -> tuple[int, ...]:
        return tuple(sorted(self._stores))
