"""Exception hierarchy for sr_engine."""


class EngineError(Exception):
    """Base class for all engine errors."""


class DimensionError(EngineError):
    """A coordinate falls outside the matrix dimensions."""


class DuplicateTxnError(EngineError):
    """A transaction id was reused with different content."""


class VersionNotRetainedError(EngineError):
    """The requested version has been garbage collected."""


class CorruptionError(EngineError):
    """Durable state (log or checkpoint) failed integrity checks."""
