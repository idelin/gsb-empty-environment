"""Explicit DFA model and language-equivalence checking.

Only the Python standard library is used; no regex or automaton package is
involved.  Both dense transition matrices and sparse (default + interval)
representations are accepted on input.
"""

from .errors import BadTargetError, IndexError as PIndexError


class Alphabet:
    """An ordered list of distinct symbols.

    Symbols may be integers, single characters or arbitrary hashable values;
    the encoded file stores their JSON representation.
    """

    def __init__(self, symbols):
        syms = list(symbols)
        if len(syms) == 0:
            raise ValueError("alphabet must contain at least one symbol")
        if len(set(map(repr, syms))) != len(syms):
            raise ValueError("duplicate symbols in alphabet")
        self.symbols = syms
        self.index = {s: i for i, s in enumerate(syms)}

    def __len__(self):
        return len(self.symbols)

    def __iter__(self):
        return iter(self.symbols)

    def __eq__(self, other):
        return isinstance(other, Alphabet) and self.symbols == other.symbols

    def symbol_id(self, symbol):
        try:
            return self.index[symbol]
        except KeyError:
            raise PIndexError(f"symbol {symbol!r} not in alphabet") from None


class DFA:
    """An explicit deterministic automaton.

    Parameters
    ----------
    alphabet:
        :class:`Alphabet` instance.
    transitions:
        Either a dense matrix (``list[list[int]]``) with one target per state
        and symbol, or a sparse dict mapping ``state -> row spec`` where a row
        spec is ``(default_target, runs)`` and ``runs`` is a list of
        ``(start_inclusive, end_exclusive, target)`` intervals in *symbol id*
        space.  Sparse runs may legitimately restate the default target; such
        entries are *explicit* and survive a later change of the default.
    start:
        Start state id.
    accepting:
        Iterable of accepting state ids.
    dead_state:
        Canonical dead/non-returning state id.  Explicit transitions into it
        are semantically distinct from "cell equal to the row default".
    """

    def __init__(self, alphabet, transitions, start, accepting, dead_state=None):
        if not isinstance(alphabet, Alphabet):
            raise TypeError("alphabet must be an Alphabet")
        self.alphabet = alphabet
        n = len(alphabet)
        nstates = len(transitions)
        if not (0 <= start < nstates):
            raise PIndexError(f"start state {start} out of range")
        self.num_states = nstates
        self.start = start
        self.accepting = frozenset(accepting)
        for q in self.accepting:
            if not (0 <= q < nstates):
                raise BadTargetError(f"accepting state {q} out of range")
        self.dead_state = dead_state
        if dead_state is not None and not (0 <= dead_state < nstates):
            raise BadTargetError(f"dead state {dead_state} out of range")

        self._dense = None
        self._sparse = None
        first = next(iter(transitions.values())) if isinstance(transitions, dict) else None
        if isinstance(transitions, dict) and first is not None and (
            not isinstance(first, list)
        ):
            self._sparse = self._normalize_sparse(transitions, nstates, n)
        else:
            self._dense = self._normalize_dense(transitions, nstates, n)

    # ---- input normalization ------------------------------------------------
    def _normalize_dense(self, transitions, nstates, n):
        table = []
        for q, row in enumerate(transitions):
            if len(row) != n:
                raise PIndexError(
                    f"state {q} row has {len(row)} entries, alphabet has {n}"
                )
            for t in row:
                self._check_target(t, nstates)
            table.append([int(x) for x in row])
        return table

    def _normalize_sparse(self, transitions, nstates, n):
        rows = {}
        for q, spec in transitions.items():
            if not (0 <= q < nstates):
                raise PIndexError(f"state id {q} out of range")
            default, runs = spec
            self._check_target(default, nstates)
            checked = []
            for lo, hi, target in runs:
                if not (0 <= lo <= hi <= n):
                    raise PIndexError(
                        f"run [{lo},{hi}) in state {q} crosses symbol range"
                    )
                self._check_target(target, nstates)
                checked.append((lo, hi, int(target)))
            rows[q] = (int(default), checked)
        return rows

    def _check_target(self, target, nstates):
        if not isinstance(target, int) or not (0 <= target < nstates):
            raise BadTargetError(f"target {target!r} is not a valid state id")

    # ---- access -------------------------------------------------------------
    def dense_table(self):
        """Materialize the full N_states x N_symbols matrix."""
        if self._dense is not None:
            return [row[:] for row in self._dense]
        n = len(self.alphabet)
        table = []
        for q in range(self.num_states):
            if q in self._sparse:
                default, runs = self._sparse[q]
                row = [default] * n
                for lo, hi, target in runs:
                    for s in range(lo, hi):
                        row[s] = target
            else:
                raise KeyError(f"no transition row supplied for state {q}")
            table.append(row)
        return table

    def sparse_rows(self):
        """Return ``{state: (default, sorted_runs)}`` with explicit cells kept."""
        if self._sparse is not None:
            return {q: (d, list(r)) for q, (d, r) in self._sparse.items()}
        rows = {}
        for q, row in enumerate(self._dense):
            runs = [(s, s + 1, t) for s, t in enumerate(row)]
            rows[q] = (row[0] if row else 0, runs)
        return rows

    def transition(self, state, symbol_id):
        return self.dense_table()[state][symbol_id]


def language_equivalent(alphabet, dfa_a, dfa_b, *, max_pairs=None):
    """Product-construction BFS.

    ``dfa_a`` / ``dfa_b`` are either :class:`DFA` instances or any object with
    ``num_states``, ``start``, ``accepting`` and ``transition(q, sym_id)``.
    Returns ``(equivalent, witness_or_None)`` where a witness is a symbol-id
    sequence on which acceptance differs.
    """
    n = len(alphabet)
    acc_a = set(dfa_a.accepting)
    acc_b = set(dfa_b.accepting)
    start_pair = (dfa_a.start, dfa_b.start)
    if (start_pair[0] in acc_a) != (start_pair[1] in acc_b):
        return False, []
    seen = {start_pair}
    queue = [(start_pair, [])]
    count = 0
    while queue:
        (qa, qb), word = queue.pop(0)
        count += 1
        if max_pairs is not None and count > max_pairs:
            raise RuntimeError("equivalence search exceeded pair budget")
        for sym in range(n):
            ta = dfa_a.transition(qa, sym)
            tb = dfa_b.transition(qb, sym)
            if not (0 <= ta < dfa_a.num_states and 0 <= tb < dfa_b.num_states):
                return False, word + [sym]
            if (ta in acc_a) != (tb in acc_b):
                return False, word + [sym]
            pair = (ta, tb)
            if pair not in seen:
                seen.add(pair)
                queue.append((pair, word + [sym]))
    return True, None
