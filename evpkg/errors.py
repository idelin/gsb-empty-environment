"""Typed errors for the evidence package pipeline."""


class EvpkgError(Exception):
    """Base class for all evidence package errors."""


class ConflictError(EvpkgError):
    """Same identity arrived with a different digest (must conflict)."""


class LateChunkError(EvpkgError):
    """Chunk belongs to a superseded manifest version."""


class ChecksumError(EvpkgError):
    """Declared digest does not match the actual chunk bytes."""


class UnknownManifestError(EvpkgError):
    """Referenced manifest (package_id, version) is not registered."""


class UnknownFileError(EvpkgError):
    """File id is not part of the manifest."""


class UnknownNotificationError(EvpkgError):
    """Outbox notification id does not exist."""


class SignoffBlocked(EvpkgError):
    """Validation failed, so the atomic sign-off must not happen."""

    def __init__(self, reasons):
        self.reasons = list(reasons)
        super().__init__("sign-off blocked: " + "; ".join(self.reasons))
