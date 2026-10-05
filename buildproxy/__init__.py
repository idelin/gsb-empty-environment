"""Explainable build file access proxy (Linux, Python 3.11 stdlib only).

All file access goes through :class:`FileProxy`, anchored at a root
directory file descriptor pinned at startup.  See README.md for the threat
model and its boundaries.
"""

from .audit import AuditEvent, ListSink
from .errors import (
    EscapeAttempt,
    PolicyDenied,
    ProxyError,
    ResolutionFailed,
    SymlinkLoop,
)
from .policy import Policy
from .proxy import FileProxy

__all__ = [
    "AuditEvent",
    "EscapeAttempt",
    "FileProxy",
    "ListSink",
    "Policy",
    "PolicyDenied",
    "ProxyError",
    "ResolutionFailed",
    "SymlinkLoop",
]
