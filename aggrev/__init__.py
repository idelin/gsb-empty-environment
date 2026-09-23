"""aggrev: versioned corrections over aggregated results.

Corrections carry their base version(s) and deltas; downstreams apply them
along a version DAG, pausing on gaps instead of misapplying. Publication,
acknowledgement and checkpoints commit atomically; re-published ids never
double-apply. An independent verifier recomputes from raw change events.
"""
from .models import GENESIS_VERSION, ChangeEvent, Correction, version_id_for
from .store import AtomicStore
from .publisher import ConflictError, Publisher, UnknownParentsError
from .downstream import Downstream, ReceiveResult
from .upstream import Upstream
from .verifier import VerificationReport, recompute, verify
from .gc import collect
from .fixtures import load_events

__all__ = [
    "GENESIS_VERSION",
    "AtomicStore",
    "ChangeEvent",
    "ConflictError",
    "Correction",
    "Downstream",
    "Publisher",
    "ReceiveResult",
    "UnknownParentsError",
    "Upstream",
    "VerificationReport",
    "collect",
    "load_events",
    "recompute",
    "verify",
    "version_id_for",
]
