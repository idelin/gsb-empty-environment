"""Exception hierarchy for the mini-autograd engine.

Every error raised by engine code derives from :class:`AutogradError` so that
callers can catch the whole family with a single ``except`` clause.
"""


class AutogradError(Exception):
    """Base class for all errors raised by the autograd engine."""


class GraphReleasedError(AutogradError):
    """A non-retained graph was driven through ``backward`` a second time."""


class VersionMismatchError(AutogradError):
    """A tensor saved in forward was mutated in place before backward."""


class BackwardAbortedError(AutogradError):
    """A backward transaction was cancelled through the ``cancel`` hook."""


class InjectedBackwardError(AutogradError):
    """Deliberately raised by the ``faulty`` test operator inside backward."""


class InplaceError(AutogradError):
    """An illegal in-place modification of a non-leaf tensor was attempted."""


class InvalidBackwardError(AutogradError):
    """Structural misuse of ``backward`` (non-scalar root, wrong graph, ...)."""


class StorageFreedError(AutogradError):
    """Code touched a storage buffer whose backing memory was recycled."""
