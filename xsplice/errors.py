"""Exception types raised by xsplice."""


class XSpliceError(Exception):
    """Base class for all xsplice errors."""


class SegmentValidationError(XSpliceError):
    """A segment is malformed or would read outside its source."""


class ShortSegmentError(XSpliceError):
    """A segment is too short for its fades and the policy is 'reject'."""


class GainConstraintError(XSpliceError):
    """The compiled global gain curve violates the total-gain constraint."""

    def __init__(self, position, value, limit, kind="power"):
        self.position = position
        self.value = value
        self.limit = limit
        self.kind = kind
        super().__init__(
            f"total {kind} gain {value:.6f} exceeds limit {limit:.6f} "
            f"at output sample {position}"
        )
