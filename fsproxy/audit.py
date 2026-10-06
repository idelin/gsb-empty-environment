"""Audit events.

An event is recorded for every public operation, whether it succeeds, is
denied, or fails.  ``objects`` carries the (device, inode) identities obtained
by fstat()ing the file descriptors the operation *actually* acted on -- never
a path looked up at check time, which a racer could have swapped since.
"""

from dataclasses import dataclass, field


@dataclass(frozen=True)
class ObjectId:
    dev: int
    ino: int


@dataclass
class AuditEvent:
    op: str                     # "read" | "create" | "replace" | "rename" | "unlink" | "list"
    path: str                   # caller-supplied path (source path for rename)
    resolved: str | None = None  # resolved root-relative path, if resolution got that far
    result: str = "error"        # "ok" | "denied" | "error"
    reason: str | None = None    # denial reason or error description
    objects: tuple = ()          # ObjectId of the objects actually accessed
    extra: dict = field(default_factory=dict)  # e.g. {"dst": ...} for rename
