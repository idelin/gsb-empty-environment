"""Semantic versions (major.minor.patch with optional prerelease) and requirements."""

from __future__ import annotations

import re
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)

_REQ_RE = re.compile(r"^(\^|~|>=|<=|>|<|=)?\s*(\S+)$")


class VersionError(ValueError):
    pass


def _cmp_prerelease(a, b):
    if not a and not b:
        return 0
    if not a:
        return 1  # a release outranks any prerelease
    if not b:
        return -1
    for x, y in zip(a, b):
        if x == y:
            continue
        x_num, y_num = x.isdigit(), y.isdigit()
        if x_num and y_num:
            return -1 if int(x) < int(y) else 1
        if x_num:
            return -1  # numeric identifiers sort before alphanumeric
        if y_num:
            return 1
        return -1 if x < y else 1
    return (len(a) > len(b)) - (len(a) < len(b))


@total_ordering
class Version:
    __slots__ = ("major", "minor", "patch", "prerelease")

    def __init__(self, major, minor, patch, prerelease=()):
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = tuple(prerelease)

    @classmethod
    def parse(cls, text):
        m = _VERSION_RE.match(text.strip())
        if not m:
            raise VersionError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        pre = tuple(m.group(4).split(".")) if m.group(4) else ()
        return cls(int(m.group(1)), int(m.group(2)), int(m.group(3)), pre)

    def _key(self):
        return (self.major, self.minor, self.patch)

    def __eq__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key() and self.prerelease == other.prerelease

    def __lt__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        if self._key() != other._key():
            return self._key() < other._key()
        return _cmp_prerelease(self.prerelease, other.prerelease) < 0

    def __hash__(self):
        return hash((self._key(), self.prerelease))

    def __str__(self):
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def __repr__(self):
        return f"Version({self!s})"


class VersionReq:
    """A single-comparator requirement: =, ^, ~, >=, <=, >, < (bare means exact)."""

    def __init__(self, op, base):
        self.op = op
        self.base = base

    @classmethod
    def parse(cls, text):
        m = _REQ_RE.match(text.strip())
        if not m:
            raise VersionError(f"invalid version requirement {text!r}")
        op = m.group(1) or "="
        return cls(op, Version.parse(m.group(2)))

    def _caret_upper(self):
        b = self.base
        if b.major > 0:
            return Version(b.major + 1, 0, 0)
        if b.minor > 0:
            return Version(0, b.minor + 1, 0)
        return Version(0, 0, b.patch + 1)

    def matches(self, version):
        b = self.base
        if self.op == "=":
            return version == b
        if self.op == "^":
            return b <= version < self._caret_upper()
        if self.op == "~":
            return b <= version < Version(b.major, b.minor + 1, 0)
        if self.op == ">=":
            return version >= b
        if self.op == "<=":
            return version <= b
        if self.op == ">":
            return version > b
        if self.op == "<":
            return version < b
        raise VersionError(f"unknown operator {self.op!r}")

    def __str__(self):
        return f"{self.op}{self.base}" if self.op != "=" else str(self.base)
