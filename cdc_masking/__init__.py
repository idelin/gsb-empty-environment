"""Consistent CDC masking with versioned pseudonym policies."""
from .engine import ChangeEvent, MaskedEvent, MaskingEngine, MaskingError
from .fixtures import load_events, load_policy, load_schema
from .policy import (
    PASSTHROUGH,
    PSEUDONYM,
    REDACT,
    FieldPolicy,
    ForeignKey,
    MappingRegistry,
    PolicyError,
    PolicyStore,
    PolicyVersion,
    SchemaRegistry,
    TablePolicy,
)
from .replay import (
    ReplayReport,
    VersionedOutput,
    check_invisible_non_interference,
    validate_replay,
)

__all__ = [
    "ChangeEvent",
    "MaskedEvent",
    "MaskingEngine",
    "MaskingError",
    "load_events",
    "load_policy",
    "load_schema",
    "PASSTHROUGH",
    "PSEUDONYM",
    "REDACT",
    "FieldPolicy",
    "ForeignKey",
    "MappingRegistry",
    "PolicyError",
    "PolicyStore",
    "PolicyVersion",
    "SchemaRegistry",
    "TablePolicy",
    "ReplayReport",
    "VersionedOutput",
    "check_invisible_non_interference",
    "validate_replay",
]
