"""Product automaton engine with lazy states and reusable power blocks.

Counting engine for the intersection of several DFAs over one shared
alphabet. The product states (tuples of component states) are created on
demand from the product start state.

Path counts of length ``n`` are computed as the start row of ``T ** n``
where ``T`` is the (multiplicity-aware) adjacency matrix of the reachable
product graph. Matrices are stored as sparse row dicts. To handle very large
``n`` cheaply we precompute, lazily and reuse across queries:

    M_i = T ** (2 ** i)
    S_i = I + T + ... + T ** (2 ** i - 1)

``M_{i+1} = M_i * M_i`` and ``S_{i+1} = S_i + M_i * S_i``.

Arithmetic is either unbounded Python integers (exact mode) or reduced
modulo a modulus (modular mode). The same engine structure backs both, but
they are exposed through distinct public classes.

All derived structures are keyed by the tuple of DFA versions together with
the sorted alphabet: any DFA modification invalidates them on next use.
"""

from __future__ import annotations

from collections import deque
from typing import Callable, Iterable, Optional, Tuple

from .dfa import DFA
from .witness import InfiniteWitness

WHITE, GRAY, BLACK = 0, 1, 2


class ProductEngine:
    def __init__(self, dfas: Iterable[DFA], mod: Optional[int] = None) -> None:
        dfas = tuple(dfas)
        if not dfas:
            raise ValueError("at least one DFA is required")
        alphabet = dfas[0].alphabet
        for dfa in dfas[1:]:
            if dfa.alphabet != alphabet:
                raise ValueError("all DFAs must share the same explicit alphabet")
        if mod is not None and (not isinstance(mod, int) or mod < 2):
            raise ValueError("modulus must be an integer >= 2")
        self._dfas: Tuple[DFA, ...] = dfas
        self.alphabet: Tuple[str, ...] = alphabet
        self.mod = mod
        self._fresh_key = None
        self._reset()
        self._ensure_fresh()

    @property
    def versions(self) -> Tuple[int, ...]:
        return tuple(dfa.version for dfa in self._dfas)

    # ----------------------------------------------------------- cache keying
    def _cache_key(self) -> Tuple[Tuple[int, ...], Tuple[str, ...]]:
        return (self.versions, self.alphabet)

    def _ensure_fresh(self) -> None:
        key = self._cache_key()
        if key != self._fresh_key:
            self._reset()
            self._fresh_key = key

    def _reset(self) -> None:
        # Lazy product graph.
        self._states = []          # state tuples, index is the row id
        self._index = {}           # tuple -> row id
        self._trans = []           # None until expanded, else list of targets
        self._accepting = []       # bool per state
        self._start_idx = self._intern(tuple(dfa.start for dfa in self._dfas))
        # Reusable power blocks.
        self._blocks = []          # M_i
        self._sum_blocks = []      # S_i
        self._count_cache = {}
        self._fully_expanded = False
        self._witness_cache: Optional[object] = None

    def _intern(self, state_tuple: Tuple) -> int:
        idx = self._index.get(state_tuple)
        if idx is None:
            idx = len(self._states)
            self._index[state_tuple] = idx
            self._states.append(state_tuple)
            self._trans.append(None)
            self._accepting.append(
                all(q in dfa.accepting for dfa, q in zip(self._dfas, state_tuple))
            )
        return idx

    def _expand(self, idx: int):
        """Lazily build the outgoing product transitions of one state."""
        row = self._trans[idx]
        if row is not None:
            return row
        row = []
        state_tuple = self._states[idx]
        for symbol in self.alphabet:
            targets = []
            dead = False
            for dfa, state in zip(self._dfas, state_tuple):
                nxt = dfa.next_state(state, symbol)
                if nxt is None:
                    dead = True
                    break
                targets.append(nxt)
            row.append(None if dead else self._intern(tuple(targets)))
        self._trans[idx] = row
        return row

    def _expand_all(self) -> None:
        if self._fully_expanded:
            return
        i = 0
        while i < len(self._states):
            self._expand(i)
            i += 1
        self._fully_expanded = True

    # ------------------------------------------------------- sparse matrix ops
    def _reduce(self, value: int) -> int:
        return value % self.mod if self.mod is not None else value

    def _vec_mul(self, vec: dict, matrix: list) -> dict:
        """Row vector times a sparse matrix stored as row dicts."""
        out: dict = {}
        for i, value in vec.items():
            for j, weight in matrix[i].items():
                out[j] = out.get(j, 0) + value * weight
        if self.mod is not None:
            for j in out:
                out[j] %= self.mod
        return out

    def _mat_mul(self, left: list, right: list) -> list:
        out = []
        for row in left:
            acc: dict = {}
            for k, value in row.items():
                for j, weight in right[k].items():
                    acc[j] = acc.get(j, 0) + value * weight
            if self.mod is not None:
                for j in acc:
                    acc[j] %= self.mod
            out.append(acc)
        return out

    def _mat_add(self, left: list, right: list) -> list:
        out = []
        for row_left, row_right in zip(left, right):
            row = dict(row_left)
            for j, weight in row_right.items():
                row[j] = row.get(j, 0) + weight
            if self.mod is not None:
                for j in row:
                    row[j] %= self.mod
            out.append(row)
        return out

    def _ensure_blocks(self, level: int) -> None:
        if not self._blocks:
            self._expand_all()
            size = len(self._states)
            base = []
            for i in range(size):
                row: dict = {}
                for target in self._trans[i]:
                    if target is not None:
                        row[target] = row.get(target, 0) + 1
                if self.mod is not None:
                    for target in row:
                        row[target] %= self.mod
                base.append(row)
            self._blocks.append(base)
            self._sum_blocks.append([{i: 1} for i in range(size)])
        while len(self._blocks) <= level:
            power = self._blocks[-1]
            prefix = self._sum_blocks[-1]
            self._blocks.append(self._mat_mul(power, power))
            self._sum_blocks.append(
                self._mat_add(prefix, self._mat_mul(power, prefix))
            )

    def _accept_dot(self, vec: dict) -> int:
        total = 0
        for i, value in vec.items():
            if self._accepting[i]:
                total += value
        return total % self.mod if self.mod is not None else total

    def _advance(self, vec: dict, length: int) -> dict:
        """Multiply ``vec`` by T ** length via binary lifting blocks."""
        level = 0
        while length:
            if length & 1:
                self._ensure_blocks(level)
                vec = self._vec_mul(vec, self._blocks[level])
            length >>= 1
            level += 1
        return vec

    # --------------------------------------------------------------- counting
    def count_length(self, length: int) -> int:
        """Number of accepted strings of exactly ``length`` symbols."""
        self._ensure_fresh()
        if length < 0:
            raise ValueError("length must be non-negative")
        cached = self._count_cache.get(length)
        if cached is not None:
            return cached
        vec = self._advance({self._start_idx: 1}, length)
        result = self._accept_dot(vec)
        self._count_cache[length] = result
        return result

    def count_from(self, state_idx: int, length: int) -> int:
        """Accepted completions of ``length`` symbols from one product state."""
        return self._accept_dot(self._advance({state_idx: 1}, length))

    def prefix_count(self, bound: int) -> int:
        """Accepted strings whose length is in ``[0, bound)``."""
        if bound <= 0:
            return 0
        self._ensure_fresh()
        vec = {self._start_idx: 1}
        total = 0
        level = 0
        while bound:
            if bound & 1:
                self._ensure_blocks(level)
                total += self._accept_dot(self._vec_mul(vec, self._sum_blocks[level]))
                vec = self._vec_mul(vec, self._blocks[level])
                if self.mod is not None:
                    total %= self.mod
            bound >>= 1
            level += 1
        return total % self.mod if self.mod is not None else total

    def count_interval(self, low: int, high: int) -> int:
        """Accepted strings whose length is in the inclusive interval."""
        if low < 0 or high < 0:
            raise ValueError("length bounds must be non-negative")
        if high < low:
            return 0
        head = self.prefix_count(high + 1)
        tail = self.prefix_count(low)
        if self.mod is not None:
            return (head - tail) % self.mod
        return head - tail

    # --------------------------------------------------- lexicographic queries
    def kth_string(self, length: int, k: int) -> str:
        """Return the k-th (0-based) accepted string of exact ``length``."""
        self._ensure_fresh()
        if length < 0:
            raise ValueError("length must be non-negative")
        if k < 0:
            raise ValueError("k must be non-negative")
        if k >= self.count_length(length):
            raise IndexError(
                f"k={k} is out of range for {self.count_length(length)} strings "
                f"of length {length}"
            )
        state_idx = self._start_idx
        pieces = []
        for position in range(length):
            remaining = length - position - 1
            row = self._expand(state_idx)
            for sym_idx, symbol in enumerate(self.alphabet):
                target = row[sym_idx]
                if target is None:
                    continue
                branch_count = self.count_from(target, remaining)
                if k < branch_count:
                    pieces.append(symbol)
                    state_idx = target
                    break
                k -= branch_count
            else:  # pragma: no cover - guarded by the range check above
                raise IndexError("internal inconsistency while selecting k-th")
        return "".join(pieces)

    def rank(self, text: str) -> Optional[int]:
        """0-based lexicographic rank among accepted strings of the same length.

        Returns ``None`` if ``text`` is not itself accepted.
        """
        self._ensure_fresh()
        state_idx = self._start_idx
        total = 0
        length = len(text)
        for position, symbol in enumerate(text):
            row = self._expand(state_idx)
            remaining = length - position - 1
            for sym_idx, alpha_symbol in enumerate(self.alphabet):
                target = row[sym_idx]
                if alpha_symbol == symbol:
                    if target is None:
                        return None
                    state_idx = target
                    break
                if target is not None:
                    total += self.count_from(target, remaining)
            else:
                raise ValueError(f"symbol {symbol!r} is not in the alphabet")
        if not self._accepting[state_idx]:
            return None
        return total

    def kth_in_interval(self, low: int, high: int, k: int) -> str:
        """k-th accepted string in [low, high], ordered by (length, lex)."""
        if k < 0:
            raise ValueError("k must be non-negative")
        for length in range(low, high + 1):
            count = self.count_length(length)
            if k < count:
                return self.kth_string(length, k)
            k -= count
        raise IndexError("k is out of range for the length interval")

    # ------------------------------------------------------- infinitude proof
    def _co_reachable_to_accepting(self) -> set:
        """Product states from which an accepting state is reachable."""
        self._expand_all()
        reverse = [set() for _ in self._states]
        for i, row in enumerate(self._trans):
            for target in row:
                if target is not None:
                    reverse[target].add(i)
        seen = {i for i, ok in enumerate(self._accepting) if ok}
        queue = deque(seen)
        while queue:
            current = queue.popleft()
            for predecessor in reverse[current]:
                if predecessor not in seen:
                    seen.add(predecessor)
                    queue.append(predecessor)
        return seen

    def _productive_cycle_state(self) -> Optional[int]:
        """Find a reachable state on a cycle that can reach acceptance.

        The search is restricted to states that can reach an accepting state,
        so a reachable but irrelevant rejecting cycle never qualifies.
        """
        self._ensure_fresh()
        self._expand_all()
        productive = self._co_reachable_to_accepting()
        start = self._start_idx
        if start not in productive:
            return None
        colors = [WHITE] * len(self._states)
        colors[start] = GRAY
        stack = [(start, 0)]
        while stack:
            node, edge_pos = stack[-1]
            row = self._trans[node]
            next_node = None
            while edge_pos < len(row):
                candidate = row[edge_pos]
                edge_pos += 1
                if candidate is not None and candidate in productive:
                    next_node = candidate
                    break
            stack[-1] = (node, edge_pos)
            if next_node is None:
                colors[node] = BLACK
                stack.pop()
            elif colors[next_node] == GRAY:
                return next_node
            elif colors[next_node] == WHITE:
                colors[next_node] = GRAY
                stack.append((next_node, 0))
        return None

    def _shortest_string(
        self, source: int, is_target: Callable[[int], bool]
    ) -> Optional[str]:
        if is_target(source):
            return ""
        previous = {source: None}
        queue = deque([source])
        while queue:
            current = queue.popleft()
            for sym_idx, target in enumerate(self._expand(current)):
                if target is None or target in previous:
                    continue
                previous[target] = (current, self.alphabet[sym_idx])
                if is_target(target):
                    pieces = []
                    node = target
                    while node != source:
                        parent, symbol = previous[node]
                        pieces.append(symbol)
                        node = parent
                    pieces.reverse()
                    return "".join(pieces)
                queue.append(target)
        return None

    def _loop_string(self, node: int) -> Optional[str]:
        """Shortest non-empty string that leaves ``node`` and returns to it."""
        previous: dict = {}
        queue = deque()
        for sym_idx, target in enumerate(self._expand(node)):
            if target is None:
                continue
            if target == node:
                return self.alphabet[sym_idx]
            previous.setdefault(target, (node, self.alphabet[sym_idx]))
            queue.append(target)
        while queue:
            current = queue.popleft()
            for sym_idx, target in enumerate(self._expand(current)):
                if target is None:
                    continue
                if target == node:
                    pieces = [self.alphabet[sym_idx]]
                    ancestor = current
                    while ancestor != node:
                        parent, symbol = previous[ancestor]
                        pieces.append(symbol)
                        ancestor = parent
                    pieces.reverse()
                    return "".join(pieces)
                if target not in previous:
                    previous[target] = (current, self.alphabet[sym_idx])
                    queue.append(target)
        return None

    def infinite_witness(self) -> Optional[InfiniteWitness]:
        """Return reachable/productive cycle evidence, or ``None`` if finite."""
        self._ensure_fresh()
        if self._witness_cache is _NO_WITNESS:
            return None
        if self._witness_cache is not None:
            return self._witness_cache
        node = self._productive_cycle_state()
        if node is None:
            self._witness_cache = _NO_WITNESS
            return None
        prefix = self._shortest_string(self._start_idx, lambda s: s == node)
        cycle = self._loop_string(node)
        suffix = self._shortest_string(node, lambda s: self._accepting[s])
        witness = InfiniteWitness(prefix=prefix, cycle=cycle, suffix=suffix)
        self._witness_cache = witness
        return witness

    def is_infinite(self) -> bool:
        return self.infinite_witness() is not None


_NO_WITNESS = object()
