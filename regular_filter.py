"""Domain filtering for finite variable sequences constrained by a DFA.

Implements arc consistency for the REGULAR constraint: a sequence of
finite-domain variables must form a word accepted by a given DFA.
Filtering uses forward-reachable and backward-reachable (co-reachable)
state sets per position; any value that can only lead to dead (non-
accepting) paths is removed.  Supports incremental domain updates,
empty sequences, and independent solution checking.

Pure standard library; no SAT/SMT solver is used.
"""

from __future__ import annotations

from itertools import product


class DFA:
    """A deterministic finite automaton with a total transition function."""

    def __init__(self, num_states, start, accepts, transitions):
        """
        transitions: dict mapping (state, symbol) -> state, total over
        states x alphabet.  The alphabet is inferred from the keys.
        """
        if not 0 <= start < num_states:
            raise ValueError("start state out of range")
        self.num_states = num_states
        self.start = start
        self.accepts = frozenset(accepts)
        for s in self.accepts:
            if not 0 <= s < num_states:
                raise ValueError("accept state out of range")
        self.transitions = dict(transitions)
        self.alphabet = frozenset(sym for (_, sym) in self.transitions)
        for state in range(num_states):
            for sym in self.alphabet:
                tgt = self.transitions.get((state, sym))
                if tgt is None:
                    raise ValueError(
                        f"transition missing for (state={state}, symbol={sym!r})"
                    )
                if not 0 <= tgt < num_states:
                    raise ValueError("transition target out of range")

    def step(self, state, symbol):
        return self.transitions[(state, symbol)]

    def accepts_word(self, word):
        """Independent check: simulate the DFA on the word."""
        state = self.start
        for symbol in word:
            state = self.step(state, symbol)
        return state in self.accepts


class RegularFilter:
    """Maintains arc-consistent domains for a DFA-constrained sequence.

    forward[i]  : states reachable from the start after reading positions
                  0..i-1 (with current domains).
    backward[i] : states at position i from which an accept state is still
                  reachable through positions i..n-1.
    A value v at position i is supported iff some state s in forward[i]
    has step(s, v) in backward[i+1].  Values without support only lead to
    dead paths and are removed.
    """

    def __init__(self, dfa, domains):
        self.dfa = dfa
        self.original_domains = [frozenset(d) for d in domains]
        for d in self.original_domains:
            unknown = d - dfa.alphabet
            if unknown:
                raise ValueError(f"domain values outside DFA alphabet: {unknown}")
        self.domains = [set(d) for d in domains]
        n = len(self.domains)
        self.forward = [set() for _ in range(n + 1)]
        self.backward = [set() for _ in range(n + 1)]
        self._propagate_full()

    def __len__(self):
        return len(self.domains)

    # -- propagation internals -------------------------------------------

    def _recompute_forward(self, from_index):
        """Recompute forward[from_index+1 .. n]; forward[from_index] is current."""
        for i in range(from_index, len(self.domains)):
            nxt = set()
            for s in self.forward[i]:
                for v in self.domains[i]:
                    nxt.add(self.dfa.step(s, v))
            self.forward[i + 1] = nxt

    def _recompute_backward(self, from_index):
        """Recompute backward[from_index-1 .. 0]; backward[from_index] is current."""
        for i in range(from_index - 1, -1, -1):
            nxt = self.backward[i + 1]
            prev = set()
            for s in range(self.dfa.num_states):
                if any(self.dfa.step(s, v) in nxt for v in self.domains[i]):
                    prev.add(s)
            self.backward[i] = prev

    def _filter_all(self):
        """Remove unsupported values at every position (one pass is exact:
        unsupported values lie on no complete path, so removing them
        invalidates no other value's support)."""
        for i in range(len(self.domains)):
            frontier = self.forward[i]
            nxt = self.backward[i + 1]
            self.domains[i] = {
                v
                for v in self.domains[i]
                if any(self.dfa.step(s, v) in nxt for s in frontier)
            }

    def _propagate_full(self):
        n = len(self.domains)
        self.forward[0] = {self.dfa.start}
        self._recompute_forward(0)
        self.backward[n] = set(self.dfa.accepts)
        self._recompute_backward(n)
        self._filter_all()

    def _propagate_from(self, index):
        """Incremental re-propagation after domains[index] changed.

        forward[index] and backward[index+1] are unaffected, so only the
        forward suffix and backward prefix are recomputed.
        """
        self._recompute_forward(index)
        self._recompute_backward(index + 1)
        self._filter_all()

    # -- public API ------------------------------------------------------

    def remove_value(self, index, value):
        """Incrementally remove a value from a domain and re-propagate."""
        self.domains[index].discard(value)
        self._propagate_from(index)

    def assign(self, index, value):
        """Incrementally fix position `index` to `value` and re-propagate."""
        if value not in self.domains[index]:
            raise ValueError(f"value {value!r} not in domain of position {index}")
        self.domains[index] = {value}
        self._propagate_from(index)

    def is_satisfiable(self):
        n = len(self.domains)
        if n == 0:
            # Empty sequence: accepted iff the start state is accepting.
            return self.dfa.start in self.dfa.accepts
        return bool(self.forward[n] & self.dfa.accepts)

    def solve(self):
        """Backtracking search using incremental propagation.

        Returns an accepted word (tuple) drawn from the original domains,
        or None if unsatisfiable.  Any claimed solution is verified by the
        independent checker against the original constraint.
        """
        if not self.is_satisfiable():
            return None
        word = []
        if self._search(0, word):
            result = tuple(word)
            assert check_solution(self.dfa, self.original_domains, result)
            return result
        return None

    def _search(self, i, word):
        n = len(self.domains)
        if i == n:
            return True
        snapshot = [set(d) for d in self.domains]
        for v in sorted(self.domains[i]):
            self.assign(i, v)
            word.append(v)
            if self.is_satisfiable() and self._search(i + 1, word):
                return True
            word.pop()
            self.domains = [set(d) for d in snapshot]
            self._propagate_full()
        return False


def check_solution(dfa, original_domains, word):
    """Independent checker: verify `word` against the ORIGINAL constraint
    (original domains + DFA acceptance), not the filtered state."""
    if len(word) != len(original_domains):
        return False
    for value, domain in zip(word, original_domains):
        if value not in domain:
            return False
    return dfa.accepts_word(word)


def brute_force_supported_values(dfa, domains):
    """Reference: enumerate every sequence in the domain product and keep
    values that appear in at least one accepted word.  Also reports whether
    any accepted word exists.  Handles the empty sequence (product yields
    the single empty tuple)."""
    n = len(domains)
    supported = [set() for _ in range(n)]
    any_accepted = False
    for word in product(*(sorted(d) for d in domains)):
        if dfa.accepts_word(word):
            any_accepted = True
            for i, v in enumerate(word):
                supported[i].add(v)
    return supported, any_accepted
