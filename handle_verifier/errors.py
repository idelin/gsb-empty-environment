"""Error kinds, certainty levels and replayable counterexample records."""
from __future__ import annotations

import enum
from dataclasses import dataclass


class ErrorKind(enum.Enum):
    CLOSE_WHILE_BORROWED = "close-while-borrowed"
    USE_AFTER_CLOSE = "use-after-close"
    USE_AFTER_MOVE = "use-after-move"
    BORROW_CONFLICT = "borrow-conflict"
    DOUBLE_CLOSE = "double-close"
    UNBOUND_VAR = "unbound-variable"
    INVALID_OPERATION = "invalid-operation"


class Certainty(enum.Enum):
    DEFINITE = "definite"    # every path reaching the point hits the error
    POSSIBLE = "possible"    # only some paths reaching the point hit it


@dataclass(frozen=True)
class ErrorRecord:
    """A counterexample: the ``choices`` sequence replays it concretely."""
    kind: ErrorKind
    certainty: Certainty
    where: str
    detail: str
    choices: tuple = ()

    def __str__(self):
        return (f"[{self.certainty.value}] {self.kind.value} "
                f"at {self.where} -- {self.detail}")
