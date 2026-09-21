"""Exception types for the coverage ledger."""


class LedgerError(Exception):
    """Base class for ledger errors."""


class InvalidWindow(LedgerError):
    """Raised when a query window is missing, reversed or zero length."""


class UnknownRecord(LedgerError):
    """Raised when an observation or membership rule id is not found."""


class DuplicateReport(LedgerError):
    """Raised on an exact repeated upload so it is never counted twice."""


class InvalidInterval(LedgerError):
    """Raised when an interval or parameter is malformed."""
