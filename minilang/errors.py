"""Control-transfer signals and language-level errors.

All language-level control flow (break / continue / return / throw) is
modelled as a subclass of ``ControlTransfer`` so that ``try`` execution can
treat every pending transfer uniformly when deciding whether ``finally``
overrides it.
"""
from __future__ import annotations


class ControlTransfer(Exception):
    """Base class for every language-level control transfer."""

    def __init__(self, start=None, end=None):
        super().__init__()
        # UTF-8 byte range of the source construct that raised the transfer.
        self.start = start
        self.end = end


class BreakSignal(ControlTransfer):
    """Raised by a ``break`` statement; caught by the innermost loop."""


class ContinueSignal(ControlTransfer):
    """Raised by a ``continue`` statement; caught by the innermost loop."""


class ReturnSignal(ControlTransfer):
    """Raised by ``return``; carries the already-evaluated return value."""

    def __init__(self, value, start=None, end=None):
        super().__init__(start, end)
        self.value = value


class LangThrow(ControlTransfer):
    """A value thrown by a ``throw`` statement (or a runtime fault).

    ``cause`` links to the pending ``LangThrow`` that this throw replaced
    when a ``finally`` block threw while another throw was in flight.
    """

    def __init__(self, value, start=None, end=None, cause=None):
        super().__init__(start, end)
        self.value = value
        self.cause = cause


class RuntimeFault(LangThrow):
    """An interpreter-detected runtime error (catchable like a throw).

    The caught value is the human-readable message string.
    """

    def __init__(self, message, start=None, end=None, cause=None):
        super().__init__(message, start, end, cause)
        self.message = message
