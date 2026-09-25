"""Capability-token based host function registration.

A caller can only register or unregister functions while holding a
Capability issued by the same registry.  Registered functions are wrapped
so that:

* they only ever receive plain values (the executor never hands host
  objects to guest code or to the wrapped call signature),
* any exception they raise is converted into a sanitized ExprError that
  carries no host details (no messages, paths or tracebacks),
* their return value is validated and normalized to plain data, so host
  objects cannot leak back into the expression world.
"""

import re

from .builtins import BUILTINS
from .errors import ExprError
from .lexer import KEYWORDS
from .values import normalize

_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*\Z")


class Capability:
    """An unforgeable token authorizing registration on one registry."""

    __slots__ = ("_owner", "label")

    def __init__(self, owner, label):
        self._owner = owner
        self.label = label

    def __repr__(self):
        return "<Capability %r>" % (self.label,)


class FunctionRegistry:
    def __init__(self):
        self._token = object()
        self._functions = {}

    def issue_capability(self, label=""):
        """Mint a new capability for this registry."""
        return Capability(self._token, label)

    def _check(self, capability):
        if not isinstance(capability, Capability) or \
                capability._owner is not self._token:
            raise PermissionError(
                "invalid capability: registration requires a capability "
                "issued by this registry")

    def register(self, capability, name, func):
        self._check(capability)
        if not isinstance(name, str) or not _NAME_RE.match(name) \
                or name in KEYWORDS:
            raise ValueError("invalid function name %r" % (name,))
        if name in BUILTINS:
            raise ValueError("cannot override builtin function %r" % name)
        if not callable(func):
            raise TypeError("func must be callable")
        self._functions[name] = _wrap_host_function(name, func)

    def unregister(self, capability, name):
        self._check(capability)
        self._functions.pop(name, None)

    def snapshot(self):
        return dict(self._functions)


def _wrap_host_function(name, func):
    def call(args, ctx):
        try:
            result = func(*args)
        except ExprError:
            raise
        except Exception:
            # The original exception (message, paths, traceback) is
            # deliberately dropped.
            raise ExprError("function '%s' raised an exception" % name,
                            ctx.start, ctx.end) from None
        return normalize(result, ctx.limits, ctx.budget,
                         "function '%s'" % name, ctx.start, ctx.end)

    return call
