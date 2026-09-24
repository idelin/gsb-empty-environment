"""Semantic versions (major.minor.patch with optional prerelease) and constraints."""

from __future__ import annotations

import re
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$"
)


def _compare_prerelease(left, right):
    if not left and not right:
        return 0
    if not left:
        return 1  # a release sorts after any of its prereleases
    if not right:
        return -1
    for a, b in zip(left, right):
        if a == b:
            continue
        a_num = a.isdigit()
        b_num = b.isdigit()
        if a_num and b_num:
            return -1 if int(a) < int(b) else 1
        if a_num:
            return -1  # numeric identifiers sort before alphanumeric ones
        if b_num:
            return 1
        return -1 if a < b else 1
    if len(left) == len(right):
        return 0
    return -1 if len(left) < len(right) else 1


@total_ordering
class Version:
    """An immutable major.minor.patch version with an optional prerelease."""

    __slots__ = ("major", "minor", "patch", "prerelease")

    def __init__(self, major, minor, patch, prerelease=()):
        self.major = major
        self.minor = minor
        self.patch = patch
        self.prerelease = tuple(prerelease)

    @classmethod
    def parse(cls, text):
        match = _VERSION_RE.match(text.strip())
        if match is None:
            raise ValueError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
        return cls(int(match.group(1)), int(match.group(2)), int(match.group(3)), prerelease)

    def __str__(self):
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(self.prerelease)
        return text

    def __repr__(self):
        return f"Version({str(self)!r})"

    def __eq__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key()

    def __lt__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if core != other_core:
            return core < other_core
        return _compare_prerelease(self.prerelease, other.prerelease) < 0

    def __hash__(self):
        return hash(self._key())

    def _key(self):
        return (self.major, self.minor, self.patch, self.prerelease)


_OPERATORS = (">=", "<=", "==", "!=", ">", "<")


class Constraint:
    """A comma-separated set of comparator clauses, e.g. ``>=1.2.0,<2.0.0``."""

    __slots__ = ("clauses",)

    def __init__(self, spec):
        clauses = []
        text = (spec or "").strip()
        if text and text != "*":
            for part in text.split(","):
                part = part.strip()
                operator = "=="
                for candidate in _OPERATORS:
                    if part.startswith(candidate):
                        operator = candidate
                        part = part[len(candidate):].strip()
                        break
                clauses.append((operator, Version.parse(part)))
        self.clauses = tuple(clauses)

    def allows(self, version):
        return all(_matches(op, version, target) for op, target in self.clauses)

    def __str__(self):
        if not self.clauses:
            return "*"
        return ",".join(f"{op}{ver}" for op, ver in self.clauses)

    def __repr__(self):
        return f"Constraint({str(self)!r})"

    def __eq__(self, other):
        if not isinstance(other, Constraint):
            return NotImplemented
        return self.clauses == other.clauses

    def __hash__(self):
        return hash(self.clauses)


def _matches(operator, version, target):
    if operator == "==":
        return version == target
    if operator == "!=":
        return version != target
    if operator == ">=":
        return version >= target
    if operator == "<=":
        return version <= target
    if operator == ">":
        return version > target
    if operator == "<":
        return version < target
    raise ValueError(f"unknown operator {operator!r}")
