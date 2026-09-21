"""Version domain: major.minor.patch with an optional prerelease tag.

A prerelease tag is a channel name plus a number, e.g. ``1.2.3b1`` is
channel ``b`` number ``1``.  The special channel ``stable`` denotes a
version without a prerelease tag.  The domain is infinite, so all set
operations are defined symbolically on interval endpoints; nothing here
enumerates the domain.
"""

from __future__ import annotations

import functools
import re

STABLE = "stable"

# Ordering rank of well known prerelease channels (PEP 440 flavoured).
_CHANNEL_RANK = {"a": 0, "alpha": 0, "b": 1, "beta": 1, "rc": 2, "c": 2}

# Channels tried, in order, when the smallest allowed prerelease channel
# of an unrestricted (``ALL``) channel set is needed.
KNOWN_CHANNELS = ("a", "b", "rc")

# Smallest prerelease number used when a fresh prerelease of a channel is
# synthesised (e.g. the successor of a stable version).
PRE_MIN = 0

_VERSION_RE = re.compile(
    r"^(\d+)\.(\d+)\.(\d+)(?:-?([A-Za-z]+)[.-]?(\d+))?$"
)


def channel_rank(channel: str) -> int:
    return _CHANNEL_RANK.get(channel, 10)


@functools.total_ordering
class Version:
    """An immutable ``major.minor.patch`` version with optional prerelease."""

    __slots__ = ("major", "minor", "patch", "channel", "pre")

    def __init__(self, major: int, minor: int, patch: int,
                 channel: str | None = None, pre: int | None = None):
        if channel == STABLE:
            channel = None
        if channel is None:
            pre = None
        elif pre is None:
            raise ValueError("prerelease channel requires a number")
        object.__setattr__(self, "major", int(major))
        object.__setattr__(self, "minor", int(minor))
        object.__setattr__(self, "patch", int(patch))
        object.__setattr__(self, "channel", channel)
        object.__setattr__(self, "pre", pre)

    def __setattr__(self, name, value):  # immutable
        raise AttributeError("Version is immutable")

    # -- construction -------------------------------------------------
    @classmethod
    def parse(cls, text: str) -> "Version":
        m = _VERSION_RE.match(text.strip())
        if not m:
            raise ValueError(f"invalid version: {text!r}")
        major, minor, patch = int(m.group(1)), int(m.group(2)), int(m.group(3))
        if m.group(4) is None:
            return cls(major, minor, patch)
        return cls(major, minor, patch, m.group(4), int(m.group(5)))

    # -- ordering -----------------------------------------------------
    def _key(self):
        if self.channel is None:
            # Stable sorts after every prerelease of the same triple.
            return (self.major, self.minor, self.patch, 1)
        return (self.major, self.minor, self.patch, 0,
                channel_rank(self.channel), self.channel, self.pre)

    def __eq__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key()

    def __lt__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() < other._key()

    def __hash__(self):
        return hash(self._key())

    # -- domain navigation --------------------------------------------
    @property
    def is_stable(self) -> bool:
        return self.channel is None

    @property
    def label(self) -> str:
        """Channel label used for channel-set membership tests."""
        return STABLE if self.channel is None else self.channel

    def successor(self) -> "Version":
        """The immediately next version in the total order.

        * ``1.2.3b4`` -> ``1.2.3b5``
        * ``1.2.3``   -> ``1.2.4a0`` (nothing lies between a stable
          release and the first prerelease of the next patch triple)
        """
        if self.channel is not None:
            return Version(self.major, self.minor, self.patch,
                           self.channel, self.pre + 1)
        return Version(self.major, self.minor, self.patch + 1, "a", PRE_MIN)

    def next_triple(self) -> tuple[int, int, int]:
        return (self.major, self.minor, self.patch + 1)

    # -- representation -------------------------------------------------
    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.channel is None:
            return base
        return f"{base}{self.channel}{self.pre}"

    def __repr__(self) -> str:
        return f"Version({self})"


MIN_VERSION = Version(0, 0, 0, "a", PRE_MIN)
