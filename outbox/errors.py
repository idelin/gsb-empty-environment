"""Exception hierarchy for the outbox pipeline."""


class PipelineError(Exception):
    """Base class for every pipeline error."""


class GapDetectedError(PipelineError):
    """A fixture batch was offered out of order (a commit sequence is missing)."""


class StaleBatchError(PipelineError):
    """A batch at or below the committed high-water mark was offered again."""


class DuplicateChangeError(PipelineError):
    """Two changes inside one logical batch share the same identity."""


class ConflictingChangeError(PipelineError):
    """The same logical change was presented twice with a different payload."""


class PlannerError(PipelineError):
    """The output planner violated its contract (bad ids, uid, or cardinality)."""


class SinkError(PipelineError):
    """Base class for sink-side delivery errors."""


class DeliveryFailedError(SinkError):
    """The sink rejected the delivery; the row remains pending and may retry."""


class AckLostError(SinkError):
    """Delivery reached the sink but its acknowledgement was lost.

    The send is retried; the sink dedupes it by idempotency id.
    """


class CrashInjected(PipelineError):
    """Raised to simulate a process crash immediately after a durable commit."""
