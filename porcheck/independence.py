"""Static independence relation over actions, derived from read/write sets.

Two distinct actions are independent iff neither writes a variable the other
reads or writes.  In particular, two actions writing the same variable are
NEVER independent (write-write conflict), and a write vs. a read of the same
variable is also a conflict.  Read-read sharing is harmless.
"""

from __future__ import annotations

from .model import Action


def independent(a: Action, b: Action) -> bool:
    if a is b:
        return False
    if a.writes & (b.reads | b.writes):
        return False
    if b.writes & (a.reads | a.writes):
        return False
    return True


def independent_of_all(action: Action, others) -> bool:
    return all(independent(action, other) for other in others if other is not action)
