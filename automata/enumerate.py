"""Budgeted, resumable lexicographic enumeration of accepted words.

When the output is (potentially) too long, the enumerator emits words in
batches of at most ``budget``.  A budget that runs out is *not* a claim that
no more words exist -- the caller receives a checkpoint and may resume
later.  Checkpoints are bound to the automaton versions and the sorted
alphabet, so resuming after a mutation (or against different constraints)
is rejected instead of silently producing wrong words.
"""

from typing import List, NamedTuple, Optional


class Checkpoint(NamedTuple):
    alphabet: tuple           # sorted alphabet the enumeration belongs to
    versions: tuple           # per-automaton versions at checkpoint time
    last_word: Optional[str]  # last emitted word (None = nothing emitted)
    emitted: int              # number of words emitted so far


class LexEnumerator:
    """Resumable generator over the accepted words in a length interval."""

    def __init__(self, counter, lo, hi, _checkpoint=None):
        counter._require_exact("enumeration")
        if lo < 0 or hi < lo:
            raise ValueError("need 0 <= lo <= hi")
        self._counter = counter
        self._lo = lo
        self._hi = hi
        self._last = None
        self._emitted = 0
        self._exhausted = False
        if _checkpoint is not None:
            self._bind(_checkpoint)

    # -- checkpointing ------------------------------------------------------
    def checkpoint(self) -> Checkpoint:
        return Checkpoint(
            alphabet=self._counter.product.alphabet,
            versions=self._counter.product.version,
            last_word=self._last,
            emitted=self._emitted,
        )

    def _bind(self, cp: Checkpoint):
        product = self._counter.product
        if cp.alphabet != product.alphabet:
            raise ValueError("checkpoint alphabet does not match the automata")
        if cp.versions != product.version:
            raise ValueError("checkpoint was taken before the automata changed")
        self._last = cp.last_word
        self._emitted = cp.emitted

    @classmethod
    def resume(cls, counter, lo, hi, checkpoint: Checkpoint) -> "LexEnumerator":
        return cls(counter, lo, hi, _checkpoint=checkpoint)

    # -- enumeration primitives ---------------------------------------------
    def _walk(self, word):
        idx = self._counter._initial_idx
        for sym in word:
            idx = self._counter._trans[idx].get(sym)
            if idx is None:
                return None
        return idx

    def _first_valid_from(self, word, idx):
        """Smallest accepted word (length-bounded) with prefix ``word``."""
        lo, hi = self._lo, self._hi
        counter = self._counter
        if len(word) >= lo and idx in counter._accept:
            return word
        if len(word) >= hi:
            return None
        for sym in counter.product.alphabet:
            nxt = counter._trans[idx].get(sym)
            if nxt is None:
                continue
            if counter._subtree_count(nxt, len(word) + 1, lo, hi) > 0:
                found = self._first_valid_from(word + sym, nxt)
                if found is not None:
                    return found
        return None

    def _successor(self, word):
        """The smallest accepted word strictly greater than ``word``."""
        hi = self._hi
        counter = self._counter
        lo = self._lo
        alphabet = counter.product.alphabet
        # try to extend: first valid word below word+sym
        if len(word) < hi:
            idx = self._walk(word)
            if idx is not None:
                for sym in alphabet:
                    nxt = counter._trans[idx].get(sym)
                    if nxt is None:
                        continue
                    if counter._subtree_count(nxt, len(word) + 1, lo, hi) > 0:
                        return self._first_valid_from(word + sym, nxt)
        # otherwise backtrack to the next sibling of some prefix
        for i in range(len(word), 0, -1):
            prefix, last = word[:i - 1], word[i - 1]
            pidx = self._walk(prefix)
            if pidx is None:
                continue
            for sym in alphabet:
                if sym <= last:
                    continue
                nxt = counter._trans[pidx].get(sym)
                if nxt is None:
                    continue
                if counter._subtree_count(nxt, i, lo, hi) > 0:
                    return self._first_valid_from(prefix + sym, nxt)
        return None

    def _next(self) -> Optional[str]:
        if self._exhausted:
            return None
        self._counter._refresh()
        if self._last is None:
            word = self._first_valid_from("", self._counter._initial_idx)
        else:
            word = self._successor(self._last)
        if word is None:
            self._exhausted = True
            return None
        self._last = word
        self._emitted += 1
        return word

    # -- public API ---------------------------------------------------------
    def take(self, budget):
        """Emit up to ``budget`` words.

        Returns ``(words, done)``.  ``done`` is True only when the language
        is provably exhausted; a spent budget never reports non-existence.
        """
        if budget < 0:
            raise ValueError("budget must be >= 0")
        words: List[str] = []
        for _ in range(budget):
            word = self._next()
            if word is None:
                return words, True
            words.append(word)
        return words, False

    def __iter__(self):
        while True:
            word = self._next()
            if word is None:
                return
            yield word
