"""Word-frequency metadata, deliberately separated from the automaton.

The automaton (dawg.py) stores only the *set* of words and answers
structural queries (membership, prefix enumeration). All frequency
information lives here, so prefix queries never touch counters and
counter updates never touch the automaton unless a word appears or
disappears entirely.
"""

from __future__ import annotations


class FrequencyTable:
    def __init__(self):
        self._counts = {}
        self._total = 0

    def add(self, word, n=1):
        if n <= 0:
            raise ValueError("n must be positive")
        self._counts[word] = self._counts.get(word, 0) + n
        self._total += n

    def remove(self, word):
        """Decrement the count of word; return True if it was present."""
        count = self._counts.get(word, 0)
        if count == 0:
            return False
        if count == 1:
            del self._counts[word]
        else:
            self._counts[word] = count - 1
        self._total -= 1
        return True

    def count(self, word):
        return self._counts.get(word, 0)

    def __contains__(self, word):
        return word in self._counts

    def __len__(self):
        return len(self._counts)

    @property
    def total(self):
        return self._total

    def items(self):
        return self._counts.items()
