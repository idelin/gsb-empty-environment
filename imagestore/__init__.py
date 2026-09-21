"""Content-addressed offline image repository.

The repository identifies packages by ``(name, version, platform)``, keeps
package metadata and archives as SHA-256 addressed objects, publishes
immutable snapshots with atomic manifest replacement and performs
generational mark/sweep garbage collection guarded by explicit leases.

All durability decisions are recorded in an append-only journal with framed,
checksummed records (see :mod:`imagestore.journal`). Process crashes are
simulated by :class:`imagestore.events.InjectedCrash`; recovery is performed
by :meth:`imagestore.repo.ImageStore.open`.

Linearisation points (the single moment an operation becomes visible)
----------------------------------------------------------------------
import          journal append of ``import`` after staged objects verified
publish         ``os.replace`` of the manifest into ``snapshots/``
set-current     journal append of ``current``
lease acquire   journal append of ``lease-acquire``
lease release   journal append of ``lease-release``
gc mark         exclusive-lock acquisition after the live set is computed
gc delete       ``os.unlink`` of one object (each deletion linearises)
journal replay  the last complete, checksum-valid record in the journal
"""

from .errors import (
    ClosureIncomplete,
    LengthMismatch,
    DigestMismatch,
    JournalCorrupt,
    LeaseInvalid,
    MetadataConflict,
    MetadataInvalid,
    SnapshotExists,
    UnknownSnapshot,
)
from .repo import ImageStore, Lease, Reader
from .events import EventBus, InjectedCrash

__all__ = [
    "ImageStore",
    "Lease",
    "Reader",
    "EventBus",
    "InjectedCrash",
    "ClosureIncomplete",
    "DigestMismatch",
    "LengthMismatch",
    "JournalCorrupt",
    "LeaseInvalid",
    "MetadataConflict",
    "MetadataInvalid",
    "SnapshotExists",
    "UnknownSnapshot",
]
