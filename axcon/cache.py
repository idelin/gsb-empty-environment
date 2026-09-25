"""Shared-subexpression cache bound to tensor versions.

A cache key includes each operand's (name, version), so applying a delta to a
tensor automatically invalidates every cached subexpression that depends on
it -- no explicit invalidation bookkeeping is required.
"""

from __future__ import annotations


class ContractionCache:
    def __init__(self):
        self._store = {}

    @staticmethod
    def key(a, b, out_axes):
        return ((a.name, a.version), (b.name, b.version), tuple(out_axes))

    def get(self, key):
        return self._store.get(key)

    def put(self, key, tensor):
        self._store[key] = tensor

    def __len__(self):
        return len(self._store)
