"""Explicit rule specifications: alphabet + transition structure.

No regular-expression or automaton library is used.  Each rule carries an
explicitly materialised transition table (a plain dict) that recognises the
trigger prefix; timing and positivity/negativity constraints are expressed
by ``require`` / ``forbid`` / ``window``.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field


def build_transition_table(
    prefix: tuple[str, ...], alphabet: tuple[str, ...]
) -> dict[tuple[int, str], int]:
    """Materialise the full transition table for prefix matching.

    States are match lengths ``0..len(prefix)``; ``len(prefix)`` is the
    accepting (trigger) state.  Fallbacks follow the longest proper
    prefix-suffix, so overlapping triggers (e.g. prefix ``("A", "A")`` on
    the stream ``A A A``) are handled correctly.
    """
    table: dict[tuple[int, str], int] = {}
    for state in range(len(prefix) + 1):
        for symbol in alphabet:
            candidate = prefix[:state] + (symbol,)
            k = min(len(prefix), state + 1)
            while k > 0 and candidate[len(candidate) - k :] != prefix[:k]:
                k -= 1
            table[(state, symbol)] = k
    return table


@dataclass(frozen=True)
class RuleSpec:
    """A finite temporal rule with an explicit transition structure.

    Semantics: whenever the ``prefix`` sequence completes at event time
    ``t``, an obligation opens over the window ``(t, t + window]``.

    - ``require``: a symbol that must occur inside the window (may be
      ``None`` for pure "no C during ..." rules).
    - ``forbid``: symbols that must not occur inside the window.
    - ``consume_witness``: if True, one ``require`` event satisfies at most
      one obligation (earliest closing first); if False, a single event may
      satisfy several obligations of this rule.  Whether "the same B
      satisfies multiple As" is therefore defined per rule.
    - ``window`` may be ``math.inf``; then only termination closes the
      obligation.
    """

    name: str
    alphabet: tuple[str, ...]
    prefix: tuple[str, ...]
    require: str | None = None
    forbid: tuple[str, ...] = ()
    window: float = math.inf
    consume_witness: bool = True
    transitions: dict[tuple[int, str], int] = field(init=False, compare=False)
    alphabet_set: frozenset = field(init=False, compare=False)
    forbid_set: frozenset = field(init=False, compare=False)

    def __post_init__(self) -> None:
        if not self.prefix:
            raise ValueError("prefix must be non-empty")
        alphabet = frozenset(self.alphabet)
        if any(s not in alphabet for s in self.prefix):
            raise ValueError("prefix symbols must belong to the alphabet")
        if self.require is not None and self.require not in alphabet:
            raise ValueError("require symbol must belong to the alphabet")
        if any(s not in alphabet for s in self.forbid):
            raise ValueError("forbid symbols must belong to the alphabet")
        if self.require is not None and self.require in self.forbid:
            raise ValueError("require symbol cannot also be forbidden")
        if not self.window > 0:
            raise ValueError("window must be positive")
        object.__setattr__(self, "alphabet_set", alphabet)
        object.__setattr__(self, "forbid_set", frozenset(self.forbid))
        object.__setattr__(
            self, "transitions", build_transition_table(self.prefix, self.alphabet)
        )
