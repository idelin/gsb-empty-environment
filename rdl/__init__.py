"""Reliable delivery layer for a multi-partition event source.

Public surface:
    DeliveryManager - durable, gap-aware delivery manager
    PartConfig      - partition configuration (modulus, capacity, ...)
    Event           - source event (partition, epoch, seq, payload)
    conflict classes and error types
"""

from .errors import (
    RDLError,
    PartitionFrozenError,
    PartitionNotFoundError,
    StaleEpochError,
    CapacityExceededError,
    SpillFailureError,
    ProofRejectedError,
    InvalidDeliveryIDError,
    MigrationError,
)
from .model import (
    Event,
    PartConfig,
    IngestResult,
    AckResult,
    PumpResult,
    GrantResult,
    RevokeResult,
    ResolveResult,
    ConflictView,
    CapacitySample,
)
from .manager import DeliveryManager

__all__ = [
    "DeliveryManager",
    "PartConfig",
    "Event",
    "IngestResult",
    "AckResult",
    "PumpResult",
    "GrantResult",
    "RevokeResult",
    "ResolveResult",
    "ConflictView",
    "CapacitySample",
    "RDLError",
    "PartitionFrozenError",
    "PartitionNotFoundError",
    "StaleEpochError",
    "CapacityExceededError",
    "SpillFailureError",
    "ProofRejectedError",
    "InvalidDeliveryIDError",
    "MigrationError",
]
