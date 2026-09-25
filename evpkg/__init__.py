"""evpkg: multi-file evidence package reception, verification and sign-off."""

from .errors import (
    ChecksumError,
    ConflictError,
    EvpkgError,
    LateChunkError,
    SignoffBlocked,
    UnknownFileError,
    UnknownManifestError,
    UnknownNotificationError,
)
from .exporter import export_package
from .manifest import FileEntry, Manifest
from .model import (
    Fact,
    Rule,
    Step,
    compute_replay_digest,
    replay,
    replay_digest,
)
from .receiver import Receiver, ValidationReport
from .sample import SamplePackage, build_sample_package
from .store import Store
from .verifier import verify_package

__all__ = [
    "ChecksumError",
    "compute_replay_digest",
    "ConflictError",
    "EvpkgError",
    "export_package",
    "Fact",
    "FileEntry",
    "LateChunkError",
    "Manifest",
    "Receiver",
    "replay",
    "replay_digest",
    "Rule",
    "SamplePackage",
    "SignoffBlocked",
    "build_sample_package",
    "Step",
    "Store",
    "UnknownFileError",
    "UnknownManifestError",
    "UnknownNotificationError",
    "ValidationReport",
    "verify_package",
]
