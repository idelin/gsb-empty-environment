"""Exception hierarchy for batchbuf."""


class BatchBufError(Exception):
    """Base class for all batchbuf errors."""


class ResourceClosedError(BatchBufError):
    """Access to a closed batch/column or a reclaimed buffer was attempted."""


class QuotaExceededError(BatchBufError):
    """A ledger charge would exceed the configured quota.

    Raised *before* any state change: the failed operation leaves the
    ledger, buffers and batches exactly as they were.
    """


class DoubleReleaseError(BatchBufError):
    """A buffer or ledger entry was released more times than acquired."""


class BorrowError(BatchBufError):
    """A borrow token was returned more times than it was taken."""
