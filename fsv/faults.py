"""Faults: abnormal program outcomes, carried as data inside states."""

from dataclasses import dataclass


@dataclass(frozen=True)
class Fault:
    """An abnormal outcome: kind, human-readable message, source location."""

    kind: str
    msg: str
    loc: str

    def __str__(self):
        return f"{self.kind}: {self.msg} @ {self.loc}"


class FaultError(Exception):
    """Internal control-flow exception used to propagate a Fault."""

    def __init__(self, kind, msg, loc):
        self.fault = Fault(kind, msg, loc)
        super().__init__(str(self.fault))
