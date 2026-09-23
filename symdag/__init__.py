"""symdag: symbolic DAG partial evaluation with exact rational arithmetic.

- hash-consed DAG of rational expressions (Fraction, no symbolic library)
- partial variable binding; unbound nodes are preserved
- domain-preserving simplification (0 * undefined is never folded to 0)
- error positions tracked via origin paths; no host eval/exec
- every rewrite emits a Certificate stating its applicable domain
- incremental invalidation of stale conclusions on binding updates
"""
from .nodes import Factory, Node, to_str
from .interp import UndefinedError, interpret
from .simplify import Certificate, Entry, Session, simplify_once

__all__ = [
    "Factory", "Node", "to_str",
    "UndefinedError", "interpret",
    "Certificate", "Entry", "Session", "simplify_once",
]
