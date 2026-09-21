"""Versions of the form major.minor.patch with an optional pre-release."""

from __future__ import annotations

import re
from dataclasses import dataclass

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$"
)

_COMPARATOR_RE = re.compile(r"^(>=|<=|==|!=|>|<)(\S+)$")


def _compare_prerelease(left: tuple[str, ...], right: tuple[str, ...]) -> int:
    if not left and not right:
        return 0
    if not left:
        return 1  # a release sorts after every one of its pre-releases
    if not right:
        return -1
    for a, b in zip(left, right):
        if a == b:
            continue
        a_num, b_num = a.isdigit(), b.isdigit()
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


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "Version":
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise ValueError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        major, minor, patch, pre = match.groups()
        return cls(
            int(major), int(minor), int(patch), tuple(pre.split(".")) if pre else ()
        )

    def __str__(self) -> str:
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(self.prerelease)
        return text

    def _core(self) -> tuple[int, int, int]:
        return (self.major, self.minor, self.patch)

    def __lt__(self, other: "Version") -> bool:
        if self._core() != other._core():
            return self._core() < other._core()
        return _compare_prerelease(self.prerelease, other.prerelease) < 0

    def __le__(self, other: "Version") -> bool:
        return self == other or self < other

    def __gt__(self, other: "Version") -> bool:
        return not self.__le__(other)

    def __ge__(self, other: "Version") -> bool:
        return not self.__lt__(other)


class VersionRange:
    """A conjunctive version range: ``*``, exact, ``^x.y.z`` or comparators."""

    def __init__(self, comparators=()):
        self.comparators = tuple(comparators)

    @classmethod
    def any(cls) -> "VersionRange":
        return cls(())

    @classmethod
    def parse(cls, text: str) -> "VersionRange":
        text = (text or "").strip()
        if text in ("", "*"):
            return cls.any()
        tokens = text.split()
        if len(tokens) == 1:
            token = tokens[0]
            if token.startswith("^"):
                base = Version.parse(token[1:])
                return cls(((">=", base), ("<", Version(base.major + 1, 0, 0))))
            if _VERSION_RE.match(token):
                return cls((("==", Version.parse(token)),))
        comparators = []
        for token in tokens:
            match = _COMPARATOR_RE.match(token)
            if not match:
                raise ValueError(f"invalid version range {text!r}")
            comparators.append((match.group(1), Version.parse(match.group(2))))
        return cls(tuple(comparators))

    def contains(self, version: Version) -> bool:
        for op, bound in self.comparators:
            if op == ">=" and not version >= bound:
                return False
            if op == "<=" and not version <= bound:
                return False
            if op == ">" and not version > bound:
                return False
            if op == "<" and not version < bound:
                return False
            if op == "==" and not version == bound:
                return False
            if op == "!=" and not version != bound:
                return False
        return True

    def __str__(self) -> str:
        if not self.comparators:
            return "*"
        return " ".join(f"{op}{bound}" for op, bound in self.comparators)
