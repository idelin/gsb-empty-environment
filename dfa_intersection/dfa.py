"""Explicit deterministic finite automaton (no regex/automata libraries).

A DFA is defined by:
  * an explicit, ordered alphabet of single-character symbols,
  * a start state,
  * a set of accepting states,
  * an explicit partial transition table ``{(state, symbol): target}``.

A missing transition means the string is rejected: the automaton behaves as
if the transition entered an implicit non-accepting sink state.

Every structural mutation bumps a monotonically increasing ``version``.
Caches and checkpoints bind to that version, so modifications invalidate
stale derived data.
"""

from __future__ import annotations

from typing import Any, Iterable, Optional, Tuple


class DFA:
    def __init__(
        self,
        alphabet: Iterable[str],
        start: Any,
        accepting: Iterable[Any] = (),
        transitions: Optional[dict] = None,
    ) -> None:
        symbols = tuple(alphabet)
        if not symbols:
            raise ValueError("alphabet must be non-empty")
        for sym in symbols:
            if not isinstance(sym, str) or len(sym) != 1:
                raise ValueError(
                    f"alphabet symbols must be single-character strings, got {sym!r}"
                )
        if len(set(symbols)) != len(symbols):
            raise ValueError("alphabet symbols must be unique")
        self._alphabet: Tuple[str, ...] = tuple(sorted(symbols))
        self._alphabet_set = frozenset(self._alphabet)
        self._start = start
        self._accepting = set(accepting)
        self._transitions: dict = {}
        for (state, symbol), target in dict(transitions or {}).items():
            self._require_symbol(symbol)
            self._transitions[(state, symbol)] = target
        self._version = 0

    # ------------------------------------------------------------------ views
    @property
    def alphabet(self) -> Tuple[str, ...]:
        return self._alphabet

    @property
    def start(self) -> Any:
        return self._start

    @property
    def accepting(self) -> frozenset:
        return frozenset(self._accepting)

    @property
    def transitions(self) -> dict:
        return dict(self._transitions)

    @property
    def version(self) -> int:
        return self._version

    # --------------------------------------------------------------- mutation
    def _require_symbol(self, symbol: str) -> None:
        if symbol not in self._alphabet_set:
            raise ValueError(f"symbol {symbol!r} is not in the alphabet")

    def add_transition(self, state: Any, symbol: str, target: Any) -> None:
        """Add or replace a transition; invalidates derived caches."""
        self._require_symbol(symbol)
        self._transitions[(state, symbol)] = target
        self._version += 1

    def remove_transition(self, state: Any, symbol: str) -> None:
        """Remove a transition (the path becomes rejecting); bumps version."""
        self._require_symbol(symbol)
        if self._transitions.pop((state, symbol), None) is not None:
            self._version += 1

    def set_accepting(self, state: Any, flag: bool = True) -> None:
        """Toggle accepting status; bumps version only on an actual change."""
        if flag:
            if state not in self._accepting:
                self._accepting.add(state)
                self._version += 1
        else:
            if state in self._accepting:
                self._accepting.discard(state)
                self._version += 1

    # ------------------------------------------------------- plain simulation
    def next_state(self, state: Any, symbol: str) -> Optional[Any]:
        """One-step transition; ``None`` means the implicit sink."""
        return self._transitions.get((state, symbol))

    def walk(self, state: Any, text: str) -> Optional[Any]:
        """Simulate ``text`` starting from ``state``.

        Returns the state reached, or ``None`` if a transition is missing.
        This deliberately uses only the raw table so it can act as an
        independent checker for counterexamples.
        """
        current = state
        for symbol in text:
            current = self._transitions.get((current, symbol))
            if current is None:
                return None
        return current

    def accepts(self, text: str) -> bool:
        """Standalone acceptance test using only this DFA's raw table."""
        end = self.walk(self._start, text)
        return end is not None and end in self._accepting


def accepts_all(dfas: Iterable[DFA], text: str) -> bool:
    """Independent acceptance check for the intersection of the DFAs."""
    return all(dfa.accepts(text) for dfa in dfas)
