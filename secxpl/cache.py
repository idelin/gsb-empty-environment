"""Permission-isolated explanation cache.

Each clearance level gets its own namespace, so an entry computed under
one permission is never served to a caller with a different permission.
Within one namespace the key is (query, projection digest): sharing
between low-equivalent worlds at the *same* clearance is safe because
their public outputs are identical by construction.
"""

from __future__ import annotations


class ExplanationCache:
    def __init__(self) -> None:
        self._namespaces: dict[int, dict] = {}

    def get(self, clearance: int, key):
        return self._namespaces.get(clearance, {}).get(key)

    def put(self, clearance: int, key, value) -> None:
        self._namespaces.setdefault(clearance, {})[key] = value

    def size(self, clearance: int) -> int:
        return len(self._namespaces.get(clearance, {}))
