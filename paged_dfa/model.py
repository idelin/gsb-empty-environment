"""In-memory DFA model and lossless row compression.

A :class:`Row` is a *default destination* plus a tuple of *interval exceptions*
``(start, length, target)`` stated in positions of the sorted alphabet.  A
query returns the interval target when the symbol position falls inside one,
otherwise the default.

The distinguished :data:`DEAD` sentinel (-1) is stored exactly like a state id
inside the exception intervals, while default selection never collapses an
explicit dead cell into a "plain" default: if a row mixes dead and live cells,
dead appears as an explicit interval.  Two equal compressed rows share one row
id (:attr:`Model.row_of`).
"""

from dataclasses import dataclass
from typing import Mapping, Sequence

DEAD = -1


@dataclass(frozen=True)
class Row:
    default: int
    exceptions: tuple[tuple[int, int, int], ...] = ()

    def target_at(self, position: int) -> int:
        for start, length, target in self.exceptions:
            if start <= position < start + length:
                return target
        return self.default

    def equals_transitions(self, other: "Row") -> bool:
        return self.default == other.default and self.exceptions == other.exceptions


@dataclass(frozen=True)
class Model:
    """Logical, alphabet-aware DFA independent of paging."""

    alphabet: tuple[int, ...]
    accepting: frozenset[int]
    rows: tuple[Row, ...]
    row_of: tuple[int, ...]

    @property
    def state_count(self) -> int:
        return len(self.row_of)

    @property
    def symbol_count(self) -> int:
        return len(self.alphabet)

    def transition(self, state: int, symbol: int) -> int:
        return self.rows[self.row_of[state]].target_at(
            self.alphabet.index(symbol)
        )

    def accepts(self, word: Sequence[int], start: int = 0) -> bool:
        state = start
        for symbol in word:
            if state == DEAD:
                break
            state = self.transition(state, symbol)
        return state in self.accepting


def encode_row(transitions: Sequence[int]) -> Row:
    """Compress one full row into a default plus interval exceptions.

    The default is the most frequent destination (smallest id wins ties).
    Every maximal run that differs from it -- including runs made of explicit
    :data:`DEAD` cells -- is emitted as an interval, so dead transitions remain
    explicit and cannot be masked by a live default.
    """
    if not transitions:
        raise ValueError("row must cover at least one symbol")
    counts: dict[int, int] = {}
    for target in transitions:
        counts[target] = counts.get(target, 0) + 1
    default = max(counts, key=lambda t: (counts[t], -t))

    exceptions: list[tuple[int, int, int]] = []
    run_start = None
    run_target = None
    for pos, target in enumerate(transitions):
        if target != default:
            if run_start is None:
                run_start, run_target = pos, target
            elif target != run_target:
                exceptions.append((run_start, pos - run_start, run_target))
                run_start, run_target = pos, target
        elif run_start is not None:
            exceptions.append((run_start, pos - run_start, run_target))
            run_start = run_target = None
    if run_start is not None:
        exceptions.append((run_start, len(transitions) - run_start, run_target))
    return Row(default, tuple(exceptions))


def build_model(
    alphabet: Sequence[int],
    table: Sequence[Sequence[int]],
    accepting: Sequence[int] = (),
) -> Model:
    """Build a shared-row model from an explicit symbol-ordered table."""
    symbols = tuple(sorted(alphabet))
    if len(symbols) != len(set(symbols)):
        raise ValueError("alphabet contains duplicate symbols")
    if not symbols:
        raise ValueError("alphabet must be non-empty")
    if not table:
        raise ValueError("table must contain at least one state")

    rows: list[Row] = []
    state_count = len(table)
    for state, raw in enumerate(table):
        if len(raw) != len(symbols):
            raise ValueError(
                f"state {state} row has {len(raw)} cells, alphabet has {len(symbols)}"
            )
        for target in raw:
            if target != DEAD and not (0 <= target < state_count):
                raise ValueError(f"state {state} targets unknown state {target}")
        rows.append(encode_row(raw))

    canonical: dict[Row, int] = {}
    unique: list[Row] = []
    row_of: list[int] = []
    for row in rows:
        rid = canonical.get(row)
        if rid is None:
            rid = len(unique)
            canonical[row] = rid
            unique.append(row)
        row_of.append(rid)
    return Model(tuple(symbols), frozenset(accepting), tuple(unique), tuple(row_of))


def enumerate_language(
    model: Model,
    *,
    max_len: int,
    start: int = 0,
) -> set[tuple[int, ...]]:
    """Enumerate every accepted word up to ``max_len`` symbols."""
    accepted: set[tuple[int, ...]] = set()
    frontier = [(start, ())]
    if start in model.accepting:
        accepted.add(())
    for _ in range(max_len):
        nxt: list[tuple[int, tuple[int, ...]]] = []
        for state, word in frontier:
            if state == DEAD:
                continue
            for sym in model.alphabet:
                target = model.transition(state, sym)
                new_word = word + (sym,)
                if target in model.accepting:
                    accepted.add(new_word)
                nxt.append((target, new_word))
        frontier = nxt
    return accepted
