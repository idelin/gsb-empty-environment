"""Online minimal acyclic deterministic dictionary automaton (minimal DAFSA).

The automaton stores a *set* of words over an explicitly supplied alphabet and
is kept minimal after every insertion and deletion.  Word-frequency metadata
lives in a separate store (`Lexicon`), never inside automaton states, because
minimal-automaton states are shared between many words.

Design notes
------------
* States are integers indexing parallel arrays `_final` / `_trans`.
* A global register maps each signature ``(final, sorted(transitions))`` to a
  unique state id (hash-consing).  Because the automaton is acyclic, two
  states recognise the same right-language iff their signatures are equal
  (children are canonical by induction), so the reachable part is always the
  minimal DFA for the stored language.
* Updates are copy-on-write: existing states are never mutated.  An update
  clones the states along the word's path conceptually and re-canonicalises
  them bottom-up; untouched shared suffixes keep pointing at the old states,
  so no other word can be polluted.
* The register may retain states that became unreachable.  `compact()` /
  `rebuild` provide a proven rebuild strategy (see README.md).

Only the Python standard library is used.  No regex or automata packages.
"""

from __future__ import annotations


class AlphabetError(ValueError):
    """Raised when a word contains symbols outside the declared alphabet."""


class MinimalAcyclicAutomaton:
    """Minimal acyclic DFA over an explicit alphabet, maintained online."""

    __slots__ = ("alphabet", "_final", "_trans", "_register", "_root")

    def __init__(self, alphabet):
        alphabet = frozenset(alphabet)
        if not alphabet:
            raise AlphabetError("alphabet must be non-empty")
        bad = [s for s in alphabet if not isinstance(s, str) or len(s) != 1]
        if bad:
            raise AlphabetError(
                "alphabet symbols must be single-character strings: %r" % (bad,)
            )
        self.alphabet = alphabet
        self._final: list[bool] = []
        self._trans: list[dict[str, int]] = []
        self._register: dict[tuple, int] = {}
        self._root = self._canonical(False, {})

    # ------------------------------------------------------------------
    # internal helpers
    # ------------------------------------------------------------------
    def _canonical(self, final: bool, trans: dict[str, int]) -> int:
        """Return the unique state id for this signature, creating it if new."""
        key = (final, tuple(sorted(trans.items())))
        sid = self._register.get(key)
        if sid is None:
            sid = len(self._final)
            self._final.append(final)
            self._trans.append(dict(trans))
            self._register[key] = sid
        return sid

    def validate(self, word: str) -> None:
        if not isinstance(word, str):
            raise TypeError("word must be a str, got %r" % type(word).__name__)
        for ch in word:
            if ch not in self.alphabet:
                raise AlphabetError(
                    "symbol %r of word %r is not in the alphabet" % (ch, word)
                )

    def _walk(self, word: str):
        """Return the state reached by `word`, or None if the path dies."""
        node = self._root
        for ch in word:
            node = self._trans[node].get(ch)
            if node is None:
                return None
        return node

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def __contains__(self, word: str) -> bool:
        node = self._walk(word)
        return node is not None and self._final[node]

    def has_prefix(self, prefix: str) -> bool:
        """True iff some stored word starts with `prefix`."""
        return self._walk(prefix) is not None

    def iter_words(self, prefix: str = ""):
        """Yield all stored words starting with `prefix`, in lexicographic order."""
        node = self._walk(prefix)
        if node is None:
            return
        stack = [(node, prefix)]
        while stack:
            node, path = stack.pop()
            if self._final[node]:
                yield path
            for ch, nxt in sorted(self._trans[node].items(), reverse=True):
                stack.append((nxt, path + ch))

    def reachable_states(self) -> int:
        """Number of states reachable from the root (size of the minimal DFA)."""
        seen = {self._root}
        stack = [self._root]
        while stack:
            node = stack.pop()
            for nxt in self._trans[node].values():
                if nxt not in seen:
                    seen.add(nxt)
                    stack.append(nxt)
        return len(seen)

    def total_states(self) -> int:
        """Number of allocated states, including unreachable register entries."""
        return len(self._final)

    def is_minimal(self) -> bool:
        """Verify the minimality invariant on the reachable part.

        Exact because children are canonical: distinct reachable states must
        have distinct (final, transitions) signatures.
        """
        seen: dict[tuple, int] = {}
        stack = [self._root]
        visited = set()
        while stack:
            node = stack.pop()
            if node in visited:
                continue
            visited.add(node)
            key = (self._final[node], tuple(sorted(self._trans[node].items())))
            if key in seen and seen[key] != node:
                return False
            seen[key] = node
            stack.extend(self._trans[node].values())
        return True

    # ------------------------------------------------------------------
    # updates (copy-on-write; existing states are never mutated)
    # ------------------------------------------------------------------
    def add(self, word: str) -> bool:
        """Insert `word`.  Return True if the language changed."""
        self.validate(word)
        path = [self._root]
        node = self._root
        depth = 0
        while depth < len(word):
            nxt = self._trans[node].get(word[depth])
            if nxt is None:
                break
            path.append(nxt)
            node = nxt
            depth += 1
        if depth == len(word) and self._final[node]:
            return False  # already present

        suffix = word[depth:]
        if suffix:
            child = self._canonical(True, {})
            for ch in reversed(suffix[1:]):
                child = self._canonical(False, {ch: child})
            new_final = self._final[node]
            new_trans = dict(self._trans[node])
            new_trans[suffix[0]] = child
        else:
            new_final = True
            new_trans = dict(self._trans[node])

        replacement = self._canonical(new_final, new_trans)
        for k in range(len(path) - 1, 0, -1):
            parent = path[k - 1]
            trans = dict(self._trans[parent])
            trans[word[k - 1]] = replacement
            replacement = self._canonical(self._final[parent], trans)
        self._root = replacement
        return True

    def remove(self, word: str) -> bool:
        """Delete `word`.  Return True if the language changed.

        Removing a word that is a prefix of longer stored words only clears
        its final flag; the longer words are untouched.
        """
        self.validate(word)
        path = [self._root]
        node = self._root
        for ch in word:
            nxt = self._trans[node].get(ch)
            if nxt is None:
                return False
            path.append(nxt)
            node = nxt
        if not self._final[node]:
            return False

        # The deepest state loses its final flag.  If it has no children it
        # becomes dead and its parent drops the transition; the death may
        # cascade upward but never past a final state or a branching state.
        if self._trans[node]:
            replacement = self._canonical(False, dict(self._trans[node]))
        else:
            replacement = None  # state dies
        for k in range(len(path) - 1, 0, -1):
            parent = path[k - 1]
            trans = dict(self._trans[parent])
            if replacement is None:
                del trans[word[k - 1]]
            else:
                trans[word[k - 1]] = replacement
            if k - 1 == 0:
                # The root always exists, even if empty.
                replacement = self._canonical(self._final[parent], trans)
            elif not trans and not self._final[parent]:
                replacement = None  # parent dies too
            else:
                replacement = self._canonical(self._final[parent], trans)
        self._root = replacement
        return True

    # ------------------------------------------------------------------
    # rebuild / compaction
    # ------------------------------------------------------------------
    @classmethod
    def rebuild(cls, words, alphabet) -> "MinimalAcyclicAutomaton":
        """Build a minimal automaton for `words` from scratch.

        This is the proven fallback strategy: replaying the online inserts on
        a fresh register yields exactly the minimal DFA (see README.md), and
        `compact` below is its online use for garbage-collecting dead states.
        """
        automaton = cls(alphabet)
        for word in words:
            automaton.add(word)
        return automaton

    def compact(self) -> "MinimalAcyclicAutomaton":
        """Return an equivalent automaton with no unreachable states.

        True garbage collection: only states reachable from the root are
        copied, ids are remapped, and the register is rebuilt from scratch.
        The language is preserved by construction (every reachable transition
        and final flag is copied verbatim).
        """
        fresh = MinimalAcyclicAutomaton(self.alphabet)
        fresh._final.clear()
        fresh._trans.clear()
        fresh._register.clear()
        remap = {}
        order = []
        queue = [self._root]
        seen = {self._root}
        while queue:
            node = queue.pop()
            order.append(node)
            for nxt in self._trans[node].values():
                if nxt not in seen:
                    seen.add(nxt)
                    queue.append(nxt)
        for old_id in order:
            remap[old_id] = len(fresh._final)
            fresh._final.append(self._final[old_id])
            fresh._trans.append({})  # placeholder, filled below
        for old_id in order:
            trans = {
                ch: remap[nxt] for ch, nxt in self._trans[old_id].items()
            }
            fresh._trans[remap[old_id]] = trans
            key = (self._final[old_id], tuple(sorted(trans.items())))
            fresh._register[key] = remap[old_id]
        fresh._root = remap[self._root]
        assert fresh.is_minimal()
        return fresh


class Lexicon:
    """Dictionary with per-word frequency metadata kept *outside* the automaton.

    The automaton stores only the word set; counts live in a plain dict.
    This separation is required for correctness: minimal-automaton states are
    shared by many words, so counts attached to states would be meaningless.
    """

    def __init__(self, alphabet):
        self.automaton = MinimalAcyclicAutomaton(alphabet)
        self._freq: dict[str, int] = {}

    def add(self, word: str, count: int = 1) -> None:
        """Add `word` `count` times; repeated adds accumulate."""
        if count < 1:
            raise ValueError("count must be >= 1")
        self.automaton.validate(word)
        if word in self._freq:
            self._freq[word] += count
        else:
            self.automaton.add(word)
            self._freq[word] = count

    def remove(self, word: str) -> bool:
        """Decrement the count; the word leaves the automaton at zero."""
        current = self._freq.get(word)
        if current is None:
            return False
        if current > 1:
            self._freq[word] = current - 1
        else:
            del self._freq[word]
            self.automaton.remove(word)
        return True

    def frequency(self, word: str) -> int:
        return self._freq.get(word, 0)

    def __contains__(self, word: str) -> bool:
        return word in self._freq

    def __len__(self) -> int:
        return len(self._freq)

    def has_prefix(self, prefix: str) -> bool:
        return self.automaton.has_prefix(prefix)

    def words_with_prefix(self, prefix: str = ""):
        """Return [(word, frequency), ...] for all words under `prefix`."""
        return [(w, self._freq[w]) for w in self.automaton.iter_words(prefix)]
