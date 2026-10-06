"""Exception hierarchy for the build-file access proxy.

All *denials* (policy, escape attempts, resource-limit trips) derive from
:class:`ProxyError`.  Ordinary filesystem failures (missing files, existing
targets, ...) surface as the usual ``OSError`` subclasses so callers can keep
using idiomatic ``except FileNotFoundError`` style handling.
"""


class ProxyError(Exception):
    """Base class for every denial raised by the proxy."""


class EscapeError(ProxyError):
    """The path (or a symlink inside it) would resolve outside the root."""


class PolicyDenied(ProxyError):
    """The resolved path is not covered by the required policy subtree."""


class SymlinkLimitExceeded(ProxyError):
    """Too many symlink hops while resolving a single path."""


class ResolutionRaceError(ProxyError):
    """A component kept changing identity while being resolved."""
