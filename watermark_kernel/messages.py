from dataclasses import dataclass


@dataclass(frozen=True)
class DataMessage:
    source: str
    epoch: str
    seq: int
    event_time: int


@dataclass(frozen=True)
class ProgressMessage:
    source: str
    epoch: str
    frontier: int
    cut_seq: int


@dataclass(frozen=True)
class Decision:
    decision_id: str
    boundary: int
