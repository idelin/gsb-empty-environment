"""Revisable alert state machine.

Two layers are managed separately:

* Truth layer  (``engine``): the real fault timeline derived from event-time
  samples and a *versioned* rule.  States: pending -> firing -> resolved,
  with ``suppressed`` as an overlay when a (versioned) suppression window
  covers a firing interval.
* Notification layer (``system`` + ``outbox``): alerts, revisions and a
  confirmable outbox.  Recomputation diffs the desired notifications
  against the alert registry and emits OPEN / RESOLVED / REVOKED events.
  Nothing is ever deleted from the audit trail; corrections are new events
  that reference the original alert id and the revision that caused them.
"""

from .models import (
    DesiredNotification,
    Episode,
    FaultState,
    NotificationKind,
    RuleVersion,
    Sample,
    SuppressionWindow,
)
from .engine import (
    compute_episodes,
    compute_states,
    desired_notifications,
    effective_samples,
    state_at,
)
from .incremental import IncrementalRule
from .outbox import Outbox, OutboxEntry, OutboxStatus, RecordingTransport, TransportError
from .system import AlertRecord, AlertSystem, AuditRecord

__all__ = [
    "AlertRecord",
    "AlertSystem",
    "AuditRecord",
    "DesiredNotification",
    "Episode",
    "FaultState",
    "IncrementalRule",
    "NotificationKind",
    "Outbox",
    "OutboxEntry",
    "OutboxStatus",
    "RecordingTransport",
    "RuleVersion",
    "Sample",
    "SuppressionWindow",
    "TransportError",
    "compute_episodes",
    "compute_states",
    "desired_notifications",
    "effective_samples",
    "state_at",
]
