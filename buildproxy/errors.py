"""Exception hierarchy for the build file access proxy."""


class ProxyError(Exception):
    """Base class for all proxy-raised (non-OSError) failures."""


class PolicyDenied(ProxyError):
    """The resolved logical path is outside the permitted subtrees."""


class EscapeAttempt(ProxyError):
    """Resolution would leave the root (absolute symlink, '..' above root,
    or an absolute caller-supplied path)."""


class SymlinkLoop(ProxyError):
    """Too many symlink hops while resolving (loop or abuse)."""


class ResolutionFailed(ProxyError):
    """The path kept changing under us; bounded retries were exhausted."""
