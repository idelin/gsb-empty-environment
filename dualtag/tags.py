"""Perturbation tags.

Every differentiation call seeds its tangents under a fresh Tag. Nested
differentiation therefore never confuses an outer perturbation for an
inner one: extraction is always by exact tag identity. tag_scope tracks
the live tags so callers (and tests) can verify that no tag state leaks
across computations, including when an exception aborts one.
"""
from __future__ import annotations

import itertools
import threading
from contextlib import contextmanager


class Tag:
    __slots__ = ("uid", "name")
    _counter = itertools.count()

    def __init__(self, name=None):
        self.uid = next(Tag._counter)
        self.name = name or f"tag{self.uid}"

    def __repr__(self):
        return f"Tag({self.name}, uid={self.uid})"


_state = threading.local()


def _stack():
    stack = getattr(_state, "tags", None)
    if stack is None:
        stack = []
        _state.tags = stack
    return stack


@contextmanager
def tag_scope(*tags):
    stack = _stack()
    stack.extend(tags)
    try:
        yield tags
    finally:
        for _ in tags:
            stack.pop()


def active_tags():
    return tuple(_stack())
