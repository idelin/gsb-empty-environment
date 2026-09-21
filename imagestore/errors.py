"""Exception hierarchy for the image repository."""


class StoreError(Exception):
    """Base class for all repository errors."""


class MetadataInvalid(StoreError):
    """Package metadata is structurally invalid."""


class MetadataConflict(StoreError):
    """Metadata for the same coordinate exists already with different content."""


class DigestMismatch(StoreError):
    """Declared digest does not match the imported content."""


class LengthMismatch(DigestMismatch):
    """Declared content length does not match the bytes supplied."""


class ClosureIncomplete(StoreError):
    """A dependency closure cannot be fully resolved.

    ``issues`` holds structured missing-item reports.  Each issue is a dict
    with keys ``kind`` (``metadata-missing``, ``content-missing`` or
    ``digest-mismatch``), ``coordinate`` and ``path`` (root -> missing node).
    """

    def __init__(self, message, issues):
        super().__init__(message)
        self.issues = issues


class SnapshotExists(StoreError):
    """A snapshot with the requested id is already published."""


class UnknownSnapshot(StoreError):
    """The referenced snapshot id does not exist."""


class LeaseInvalid(StoreError):
    """The lease token is unknown or already terminated."""


class JournalCorrupt(StoreError):
    """The journal contains corruption in the middle of the log."""


class PendingGC(StoreError):
    """A garbage collection for an earlier generation is still in progress."""
