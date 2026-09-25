"""Error types for the dictionary merge engine."""


class MergeCancelled(Exception):
    """Raised when a merge job is cancelled; the job can be resumed later."""


class MergeError(Exception):
    """A data error encountered during merge, located by batch and row."""

    def __init__(self, batch_index, row, code, message):
        self.batch_index = batch_index
        self.row = row
        self.code = code
        super().__init__(
            f"batch {batch_index} row {row}: {message} (code={code})"
        )


class TermRetractedError(Exception):
    """Raised when encoding a value whose term has been retracted."""


class VersionMismatchError(Exception):
    """Raised when decoding a batch against the wrong dictionary version."""
