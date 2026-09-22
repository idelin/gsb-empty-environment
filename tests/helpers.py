"""Shared transducer builders for the test-suite."""
from __future__ import annotations

import random

from transducers import Transducer


def dedup(alphabet=("a", "b", "x")):
    """Stateful filter: drops a symbol equal to the previous kept symbol."""
    states = {"start"} | {f"seen:{s}" for s in alphabet}
    transitions = {}
    for state in states:
        for symbol in alphabet:
            if state == "start" or state != f"seen:{symbol}":
                transitions[(state, symbol)] = (f"seen:{symbol}", (symbol,))
            else:
                transitions[(state, symbol)] = (state, ())  # epsilon: drop
    return Transducer(
        states=states,
        alphabet_in=alphabet,
        alphabet_out=alphabet,
        initial="start",
        finals=states,
        transitions=transitions,
        name="dedup",
    )


def expander():
    """One-to-many: 'a' -> ('a','x'), 'b' -> ('b',), 'c' -> epsilon."""
    return Transducer.mapper(
        {"a", "b", "c"},
        {"a", "b", "x"},
        lambda s: {"a": ("a", "x"), "b": ("b",), "c": ()}[s],
        name="expander",
    )


def terminator(alphabet=("a", "b", "x"), marker="END"):
    """Identity transducer that appends an end marker at end-of-stream."""
    alphabet = tuple(alphabet)
    transitions = {("q", s): ("q", (s,)) for s in alphabet}
    return Transducer(
        states={"q"},
        alphabet_in=alphabet,
        alphabet_out=alphabet + (marker,),
        initial="q",
        finals={"q"},
        transitions=transitions,
        final_outputs={"q": (marker,)},
        name="terminator",
    )


def parity_gate():
    """Accepts only an even number of 'a's; echoes everything."""
    transitions = {
        ("even", "a"): ("odd", ("a",)),
        ("odd", "a"): ("even", ("a",)),
        ("even", "b"): ("even", ("b",)),
        ("odd", "b"): ("odd", ("b",)),
    }
    return Transducer(
        states={"even", "odd"},
        alphabet_in={"a", "b"},
        alphabet_out={"a", "b"},
        initial="even",
        finals={"even"},
        transitions=transitions,
        name="parity_gate",
    )


def random_transducer(rng: random.Random, alphabet, n_states, max_outs, name="rand"):
    """A random total deterministic transducer; all states accepting."""
    alphabet = tuple(alphabet)
    states = tuple(f"s{i}" for i in range(n_states))
    transitions = {}
    for state in states:
        for symbol in alphabet:
            nxt = rng.choice(states)
            outs = tuple(rng.choice(alphabet) for _ in range(rng.randint(0, max_outs)))
            transitions[(state, symbol)] = (nxt, outs)
    final_outputs = {}
    for state in states:
        if rng.random() < 0.3:
            final_outputs[state] = tuple(
                rng.choice(alphabet) for _ in range(rng.randint(0, max_outs))
            )
    return Transducer(
        states=states,
        alphabet_in=alphabet,
        alphabet_out=alphabet,
        initial=states[0],
        finals=states,
        transitions=transitions,
        final_outputs=final_outputs,
        name=name,
    )
