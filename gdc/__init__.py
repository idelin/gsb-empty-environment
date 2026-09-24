"""gdc: 按组维护值支持计数与 distinct 数的增量聚合器。"""
from .aggregator import (
    DistinctConfig,
    GroupedDistinctCounter,
    MissingEventError,
    NULL,
    NullPolicy,
)
from .events import ChangeEvent, DELETE, INSERT, UPDATE
from .reference import ReferenceModel

__all__ = [
    "ChangeEvent",
    "DELETE",
    "INSERT",
    "UPDATE",
    "DistinctConfig",
    "GroupedDistinctCounter",
    "MissingEventError",
    "NULL",
    "NullPolicy",
    "ReferenceModel",
]
