"""Exception hierarchy for minigrad."""


class AutogradError(Exception):
    """Base class for all minigrad errors."""


class StaleInputError(AutogradError):
    """A tensor saved for backward was modified in-place after the forward pass."""


class GraphReleasedError(AutogradError):
    """The graph has been released (or backward already consumed it)."""


class BackwardCancelled(AutogradError):
    """The backward transaction was cancelled via a CancelToken."""


class GraphStructureError(AutogradError):
    """The requested backward output does not belong to this graph."""
