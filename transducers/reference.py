"""Non-streaming reference semantics for transducer composition.

This module defines what the streaming pipeline *means*: the composition of
a pipeline of transducers is the function obtained by running each
transducer over the whole intermediate string in turn.  The streaming
implementation in :mod:`transducers.pipeline` must agree with this reference
for every input, every buffer capacity and every chunking of the input.
"""
from __future__ import annotations

from .core import NotAccepted

__all__ = ["run_transducer", "compose"]


def run_transducer(transducer, items):
    """Run one transducer over a whole sequence; return the output list."""
    state = transducer.initial
    out = []
    for item in items:
        state, outputs = transducer.step(state, item)
        out.extend(outputs)
    out.extend(transducer.final_output(state))
    if not transducer.accepts(state):
        raise NotAccepted(
            f"{transducer.name}: rests in non-final state {state!r}"
        )
    return out


def compose(transducers, items):
    """Compose transducers left to right over whole intermediate strings."""
    result = list(items)
    for transducer in transducers:
        result = run_transducer(transducer, result)
    return result
