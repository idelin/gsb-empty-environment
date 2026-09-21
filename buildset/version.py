"""Semantic versions: major.minor.patch with an optional pre-release tag.

Only the standard library is used. Pre-release precedence follows semver:
a pre-release is lower than the associated release, numeric identifiers
compare numerically and are lower than alphanumeric ones.
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-((?:0|[1-9]\d*|[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9]\d*|[A-Za-z-][0-9A-Za-z-]*))*))?$"
)

_OPERATORS = (">=", "<=", "==", "!=", ">", "<")


def _prerelease_lt(left: tuple, right: tuple) -> bool:
    if not left and not right:
        return False
    if not left:
        return False  # a release is higher than any of its pre-releases
    if not right:
        return True
    for a, b in zip(left, right):
        if a == b:
            continue
        a_num = isinstance(a, int)
        b_num = isinstance(b, int)
        if a_num and b_num:
            return a < b
        if a_num:
            return True  # numeric identifiers sort below alphanumeric
        if b_num:
            return False
        return a < b
    return len(left) < len(right)


@total_ordering
@dataclass(frozen=True, eq=False)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise ValueError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        major, minor, patch, pre = match.groups()
        identifiers = (
            tuple(int(p) if p.isdigit() else p for p in pre.split(".")) if pre else ()
        )
        return cls(int(major), int(minor), int(patch), identifiers)

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(str(p) for p in self.prerelease)
        return base

    def _key(self) -> tuple:
        return (self.major, self.minor, self.patch)

    def __eq__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._key() == other._key() and self.prerelease == other.prerelease

    def __lt__(self, other) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        if self._key() != other._key():
            return self._key() < other._key()
        return _prerelease_lt(self.prerelease, other.prerelease)

    def __hash__(self) -> int:
        return hash((self._key(), self.prerelease))


class Constraint:
    """Version clauses separated by commas or whitespace, e.g. ``>=1.2.0 <2.0.0``."""

    def __init__(self, clauses: tuple = ()):
        self.clauses = tuple(clauses)

    @classmethod
    def any(cls) -> "Constraint":
        return cls(())

    @classmethod
    def parse(cls, text: str) -> "Constraint":
        text = (text or "").strip()
        if not text or text == "*":
            return cls.any()
        tokens = text.replace(",", " ").split()
        clauses = []
        index = 0
        while index < len(tokens):
            token = tokens[index]
            if token in _OPERATORS:  # operator separated from its version
                index += 1
                token += tokens[index]
            for op in _OPERATORS:
                if token.startswith(op):
                    clauses.append((op, Version.parse(token[len(op):].strip())))
                    break
            else:
                clauses.append(("==", Version.parse(token)))
            index += 1
        return cls(tuple(clauses))

    def allows(self, version: Version) -> bool:
        for op, bound in self.clauses:
            if op == "==" and not version == bound:
                return False
            if op == "!=" and not version != bound:
                return False
            if op == ">=" and not version >= bound:
                return False
            if op == "<=" and not version <= bound:
                return False
            if op == ">" and not version > bound:
                return False
            if op == "<" and not version < bound:
                return False
        return True

    def __str__(self) -> str:
        if not self.clauses:
            return "*"
        return ",".join(f"{op}{ver}" for op, ver in self.clauses)

    def __repr__(self) -> str:
        return f"Constraint({str(self)!r})"
