"""Multi-output local build cache (stdlib only, Python 3.11)."""

from .version import Version
from .manifest import (
    Package,
    BuildOutput,
    BuildNode,
    Manifest,
    load_manifest,
    OutputContent,
)
from .store import ContentAddressableStore
from .errors import (
    BuildCacheError,
    ManifestError,
    MissingOutputError,
    DigestMismatchError,
    NonDeterministicConflictError,
)
from .engine import ActionCache, BuildEngine, BuildResult, Builder, StagingArea, SimulatorBuilder

__all__ = [
    "Version",
    "Package",
    "BuildOutput",
    "BuildNode",
    "Manifest",
    "load_manifest",
    "OutputContent",
    "ContentAddressableStore",
    "BuildCacheError",
    "ManifestError",
    "MissingOutputError",
    "DigestMismatchError",
    "NonDeterministicConflictError",
    "ActionCache",
    "BuildEngine",
    "BuildResult",
    "Builder",
    "StagingArea",
    "SimulatorBuilder",
]
