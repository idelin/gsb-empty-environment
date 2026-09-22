"""Exception hierarchy for the paged compressed DFA table."""


class PagedFAError(Exception):
    """Base class for all paged-DFA errors."""


class IndexError(PagedFAError):
    """A state or symbol index is outside the declared alphabet/state range."""


class BadTargetError(PagedFAError):
    """A transition target is not a valid state id."""


class BadDirectoryError(PagedFAError):
    """Base class for directory / offset validation failures."""


class CorruptOffsetError(BadDirectoryError):
    """An on-disk offset is out of bounds or overlaps another record."""


class BadChecksumError(BadDirectoryError):
    """A CRC32 checksum does not match the stored payload."""


class MissingPageError(PagedFAError):
    """A requested page chunk is absent from the container."""

    def __init__(self, state, page, message=None):
        self.state = state
        self.page = page
        super().__init__(message or f"missing page (state={state}, page={page})")


class LoadCancelledError(PagedFAError):
    """A page provider deliberately refused to load a page."""

    def __init__(self, state, page, reason="cancelled"):
        self.state = state
        self.page = page
        self.reason = reason
        super().__init__(f"page load cancelled (state={state}, page={page}): {reason}")


class PatchError(PagedFAError):
    """A page delta cannot be applied to the page it targets."""
