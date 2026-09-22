"""Exception hierarchy for the paged DFA container."""


class ContainerError(Exception):
    """Base class for every container error."""


class IntegrityError(ContainerError):
    """The on-disk header / index fails structural or checksum validation.

    A query can never be opened over an image that raises this: the index is
    verified before any access is handed out.
    """


class InvalidPage(IntegrityError):
    """A page blob is corrupted, malformed, or references a bad state."""


class MissingPage(ContainerError):
    """The backend has no blob for a page id that the index requires."""


class PageLoadCancelled(ContainerError):
    """The page fetcher refused to deliver the requested page."""
