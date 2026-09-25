"""Tunable parameters of the repair engine."""

from dataclasses import dataclass


@dataclass(frozen=True)
class RepairConfig:
    max_gap: int = 8
    ar_order: int = 4
    window: int = 32
    min_history: int = 6
