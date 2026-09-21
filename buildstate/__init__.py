"""Event-sourced build state tracker.

Reconstructs per-node build state from an unordered, possibly duplicated
stream of local events.  Terminal states require complete evidence; anything
short of that is reported as PENDING rather than guessed.
"""

from .version import Version, VersionError
from .events import Event, EventError, EVENT_TYPES
from .nodes import NodeSpec
from .loader import load_packages, load_events, LoadError
from .reducer import BuildState, NodeStatus, PENDING, SUCCEEDED, FAILED

__all__ = [
    "Version",
    "VersionError",
    "Event",
    "EventError",
    "EVENT_TYPES",
    "NodeSpec",
    "load_packages",
    "load_events",
    "LoadError",
    "BuildState",
    "NodeStatus",
    "PENDING",
    "SUCCEEDED",
    "FAILED",
]
