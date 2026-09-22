"""Explicit finite-state transducers (deterministic GSMs) with epsilon outputs.

A transducer here is a deterministic generalized sequential machine: every
``(state, input symbol)`` pair has at most one transition, and each transition
emits a *tuple* of output symbols.  The empty tuple is the epsilon output, so
one input symbol may produce zero, one, or many output symbols.

Alphabets, states and the transition structure are supplied explicitly by the
caller.  Nothing is derived from regular expressions and no automata library
is used; only the Python standard library is required.
"""
from __future__ import annotations

__all__ = [
    "Transducer",
    "TransducerError",
    "RejectedInput",
    "NotAccepted",
]


class TransducerError(Exception):
    """Base class for transducer failures."""


class RejectedInput(TransducerError):
    """Raised when a symbol has no transition from the current state."""


class NotAccepted(TransducerError):
    """Raised at end-of-stream when a stage rests in a non-final state."""


class Transducer:
    """A deterministic finite-state transducer with epsilon outputs.

    Parameters
    ----------
    states:
        Iterable of hashable state names.
    alphabet_in, alphabet_out:
        Iterables of hashable input/output symbols.
    initial:
        The start state; must be a member of ``states``.
    finals:
        Accepting states; end-of-stream is only valid in one of these.
    transitions:
        Mapping ``(state, in_symbol) -> (next_state, out_symbols)`` where
        ``out_symbols`` is an iterable of output symbols, possibly empty
        (epsilon output).
    final_outputs:
        Optional mapping ``state -> out_symbols`` emitted when the input
        stream ends while the machine rests in ``state``.
    """

    __slots__ = (
        "states",
        "alphabet_in",
        "alphabet_out",
        "initial",
        "finals",
        "name",
        "_transitions",
        "_final_outputs",
        "_max_outputs_per_step",
    )

    def __init__(
        self,
        *,
        states,
        alphabet_in,
        alphabet_out,
        initial,
        finals,
        transitions,
        final_outputs=None,
        name=None,
    ):
        self.states = frozenset(states)
        self.alphabet_in = frozenset(alphabet_in)
        self.alphabet_out = frozenset(alphabet_out)
        self.name = name or "transducer"
        if not self.states:
            raise ValueError(f"{self.name}: states must be non-empty")
        if initial not in self.states:
            raise ValueError(f"{self.name}: initial state {initial!r} not in states")
        self.initial = initial
        self.finals = frozenset(finals)
        unknown_finals = self.finals - self.states
        if unknown_finals:
            raise ValueError(f"{self.name}: final states not in states: {sorted(unknown_finals)!r}")

        normalised = {}
        for key, value in transitions.items():
            try:
                src, symbol = key
            except (TypeError, ValueError):
                raise ValueError(f"{self.name}: transition key must be (state, symbol), got {key!r}")
            dst, outputs = value
            outputs = tuple(outputs)
            if src not in self.states:
                raise ValueError(f"{self.name}: transition source {src!r} not in states")
            if dst not in self.states:
                raise ValueError(f"{self.name}: transition target {dst!r} not in states")
            if symbol not in self.alphabet_in:
                raise ValueError(f"{self.name}: input symbol {symbol!r} not in alphabet_in")
            bad = [o for o in outputs if o not in self.alphabet_out]
            if bad:
                raise ValueError(f"{self.name}: output symbols not in alphabet_out: {bad!r}")
            normalised[(src, symbol)] = (dst, outputs)
        self._transitions = normalised

        final_outs = {}
        for state, outputs in (final_outputs or {}).items():
            outputs = tuple(outputs)
            if state not in self.states:
                raise ValueError(f"{self.name}: final_outputs state {state!r} not in states")
            bad = [o for o in outputs if o not in self.alphabet_out]
            if bad:
                raise ValueError(f"{self.name}: final output symbols not in alphabet_out: {bad!r}")
            final_outs[state] = outputs
        self._final_outputs = final_outs

        widths = [len(outs) for _, outs in normalised.values()]
        widths += [len(outs) for outs in final_outs.values()]
        self._max_outputs_per_step = max(widths, default=0)

    # ------------------------------------------------------------------ API
    def step(self, state, symbol):
        """Return ``(next_state, output_tuple)`` for one input symbol.

        Raises :class:`RejectedInput` when no transition is defined.
        """
        try:
            return self._transitions[(state, symbol)]
        except KeyError:
            raise RejectedInput(
                f"{self.name}: no transition from state {state!r} on symbol {symbol!r}"
            ) from None

    def final_output(self, state):
        """Output tuple emitted at end-of-stream in ``state`` (possibly empty)."""
        return self._final_outputs.get(state, ())

    def accepts(self, state):
        """Whether resting in ``state`` at end-of-stream is valid."""
        return state in self.finals

    def max_outputs_per_step(self):
        """Upper bound on symbols emitted for a single input (or at EOS)."""
        return self._max_outputs_per_step

    def output_symbols(self):
        """All output symbols this machine can ever emit."""
        symbols = set()
        for _, outs in self._transitions.values():
            symbols.update(outs)
        for outs in self._final_outputs.values():
            symbols.update(outs)
        return symbols

    # ------------------------------------------------------- conveniences
    @classmethod
    def filter(cls, alphabet, keep, *, name=None):
        """Build a one-state filter: kept symbols echo, others map to epsilon."""
        transitions = {}
        for symbol in alphabet:
            transitions[("q", symbol)] = ("q", (symbol,) if keep(symbol) else ())
        return cls(
            states={"q"},
            alphabet_in=alphabet,
            alphabet_out=alphabet,
            initial="q",
            finals={"q"},
            transitions=transitions,
            name=name or "filter",
        )

    @classmethod
    def mapper(cls, alphabet_in, alphabet_out, fn, *, name=None):
        """Build a one-state transducer mapping each symbol via ``fn``.

        ``fn`` receives an input symbol and returns an iterable of output
        symbols (empty for epsilon).
        """
        transitions = {}
        for symbol in alphabet_in:
            transitions[("q", symbol)] = ("q", tuple(fn(symbol)))
        return cls(
            states={"q"},
            alphabet_in=alphabet_in,
            alphabet_out=alphabet_out,
            initial="q",
            finals={"q"},
            transitions=transitions,
            name=name or "mapper",
        )

    def __repr__(self):  # pragma: no cover - debugging aid
        return (
            f"Transducer(name={self.name!r}, states={len(self.states)}, "
            f"transitions={len(self._transitions)})"
        )
