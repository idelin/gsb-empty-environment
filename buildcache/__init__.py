"""Multi-output build cache (standard library only, offline by design)."""

from .cache import (
    BuildCache,
    CacheError,
    IntegrityError,
    MissingOutputError,
    NonDeterministicConflict,
    PublishResult,
)
from .coordinator import BuildCoordinator, BuildOutcome, BuildStatus
from .digest import build_key, input_digest
from .manifest import (
    Manifest,
    ManifestError,
    NodeSpec,
    Package,
    load_manifest,
    parse_manifest,
)
from .version import InvalidVersion, Version

__all__ = [
    "BuildCache",
    "BuildCoordinator",
    "BuildOutcome",
    "BuildStatus",
    "CacheError",
    "IntegrityError",
    "InvalidVersion",
    "Manifest",
    "ManifestError",
    "MissingOutputError",
    "NodeSpec",
    "NonDeterministicConflict",
    "Package",
    "PublishResult",
    "build_key",
    "input_digest",
    "load_manifest",
    "parse_manifest",
    "Version",
]
