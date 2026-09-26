"""Bitemporal rectangle index: exact time, versioned corrections, provenance."""

from .timekit import Duration, SyntheticOffsetTable, TimePoint, WallClockTime
from .store import (
    BitemporalStore,
    ChangePriority,
    Contender,
    Explanation,
    FragmentView,
    RawRectangle,
    Retract,
    Upsert,
)
from .verify import VerifyReport, verify_store

__all__ = [
    "BitemporalStore",
    "ChangePriority",
    "Contender",
    "Duration",
    "Explanation",
    "FragmentView",
    "RawRectangle",
    "Retract",
    "SyntheticOffsetTable",
    "TimePoint",
    "Upsert",
    "VerifyReport",
    "WallClockTime",
    "verify_store",
]
