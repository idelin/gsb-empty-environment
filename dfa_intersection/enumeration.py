"""Resumable string enumeration with version/alphabet-bound checkpoints."""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Optional, Tuple

from .errors import CheckpointError


@dataclass(frozen=True)
class Checkpoint:
    """Opaque continuation token.

    It binds the enumeration to the exact DFA versions and the sorted
    alphabet, so stale continuations cannot silently produce wrong results.
    """

    alphabet: Tuple[str, ...]
    versions: Tuple[int, ...]
    length: int
    next_index: int

    def dumps(self) -> str:
        return json.dumps(
            {
                "alphabet": list(self.alphabet),
                "versions": list(self.versions),
                "length": self.length,
                "next_index": self.next_index,
            },
            sort_keys=True,
        )

    @classmethod
    def loads(cls, raw: str) -> "Checkpoint":
        data = json.loads(raw)
        return cls(
            alphabet=tuple(data["alphabet"]),
            versions=tuple(data["versions"]),
            length=int(data["length"]),
            next_index=int(data["next_index"]),
        )


class StringEnumerator:
    """Lazy lexicographic enumerator with an output budget.

    Iteration produces accepted strings of one fixed length. When the budget
    is exhausted the enumerator stops *without* claiming the language is
    exhausted: ``done`` stays ``False`` and ``checkpoint()`` yields a token
    that resumes exactly where it stopped.
    """

    def __init__(self, counter, length: int, budget: Optional[int] = None,
                 checkpoint: Optional[Checkpoint] = None) -> None:
        if length < 0:
            raise ValueError("length must be non-negative")
        if budget is not None and budget < 0:
            raise ValueError("budget must be non-negative or None")
        if checkpoint is not None:
            if tuple(checkpoint.alphabet) != counter.alphabet:
                raise CheckpointError(
                    "checkpoint alphabet does not match the counter's sorted alphabet"
                )
            if tuple(checkpoint.versions) != counter.versions:
                raise CheckpointError(
                    "automata were modified after this checkpoint was created"
                )
            if checkpoint.length != length:
                raise CheckpointError("checkpoint length does not match")
            if checkpoint.next_index < 0:
                raise CheckpointError("checkpoint index is invalid")
            self._next = checkpoint.next_index
        else:
            self._next = 0
        self._counter = counter
        self._length = length
        self._budget = budget
        self._total = counter.count_length(length)
        if self._next > self._total:
            raise CheckpointError("checkpoint index is past the end of enumeration")

    @property
    def total(self) -> int:
        return self._total

    @property
    def produced(self) -> int:
        return self._next

    @property
    def remaining(self) -> int:
        return self._total - self._next

    @property
    def done(self) -> bool:
        """True only when every accepted string has actually been produced."""
        return self._next >= self._total

    def checkpoint(self) -> Optional[Checkpoint]:
        if self.done:
            return None
        return Checkpoint(
            alphabet=self._counter.alphabet,
            versions=self._counter.versions,
            length=self._length,
            next_index=self._next,
        )

    def __iter__(self):
        while self._next < self._total:
            if self._budget is not None and self._budget <= 0:
                # Budget exhausted: stop early, never report completion.
                return
            yield self._counter.kth_string(self._length, self._next)
            self._next += 1
            if self._budget is not None:
                self._budget -= 1
