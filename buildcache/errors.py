"""Exception hierarchy for the build cache."""

from __future__ import annotations


class BuildCacheError(Exception):
    """Base class for every cache-level failure."""


class ManifestError(BuildCacheError):
    """The local JSON manifest is missing fields or has invalid values."""


class MissingOutputError(BuildCacheError):
    """A build did not materialize one of its declared outputs."""


class DigestMismatchError(BuildCacheError):
    """A staged output is empty or its digest fails the declared check."""


class NonDeterministicConflictError(BuildCacheError):
    """The same key was built twice with different output content.

    The first published entry always wins; the later content is rejected
    and never overwrites it.
    """

    def __init__(self, key: str, existing: str, produced: str) -> None:
        self.key = key
        self.existing_digest = existing
        self.produced_digest = produced
        super().__init__(
            f"non-deterministic outputs for {key!r}: "
            f"published {existing}, rebuild produced {produced}; "
            "keeping the first result"
        )
