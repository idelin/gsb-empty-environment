"""Semantic versions: major.minor.patch with optional pre-release tags."""

from __future__ import annotations

from functools import total_ordering

_PRE_RANK = {"a": 0, "alpha": 0, "b": 1, "beta": 1, "rc": 2}


@total_ordering
class Version:
    __slots__ = ("major", "minor", "patch", "pre", "_pre_key")

    def __init__(self, major: int, minor: int, patch: int,
                 pre: tuple[str, int] | None = None) -> None:
        self.major = major
        self.minor = minor
        self.patch = patch
        self.pre = pre
        self._pre_key = _pre_key(pre)

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise ValueError(f"version must be a string, got {text!r}")
        core, sep, tail = text.strip().partition("-")
        parts = core.split(".")
        if len(parts) != 3:
            raise ValueError(f"expected major.minor.patch, got {text!r}")
        try:
            major, minor, patch = (int(p) for p in parts)
        except ValueError as exc:
            raise ValueError(f"non-numeric version component in {text!r}") from exc
        if major < 0 or minor < 0 or patch < 0:
            raise ValueError(f"negative version component in {text!r}")
        pre = None
        if sep:
            ident = tail.strip()
            if not ident:
                raise ValueError(f"empty pre-release tag in {text!r}")
            tag, num = _parse_pre(ident)
            pre = (tag, num)
        return cls(major, minor, patch, pre)

    @property
    def is_prerelease(self) -> bool:
        return self.pre is not None

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return (self.major, self.minor, self.patch, self._pre_key) == (
            other.major, other.minor, other.patch, other._pre_key)

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return (self.major, self.minor, self.patch, self._pre_key) < (
            other.major, other.minor, other.patch, other._pre_key)

    def __hash__(self) -> int:
        return hash((self.major, self.minor, self.patch, self.pre))

    def __str__(self) -> str:
        if self.pre is None:
            return f"{self.major}.{self.minor}.{self.patch}"
        return f"{self.major}.{self.minor}.{self.patch}-{self.pre[0]}{self.pre[1]}"

    def __repr__(self) -> str:
        return f"Version({self})"


def _pre_key(pre: tuple[str, int] | None) -> tuple[int, int, int]:
    # Any pre-release sorts below the final release of the same core.
    if pre is None:
        return (1, 0, 0)
    rank, num = _PRE_RANK[pre[0]], pre[1]
    return (0, rank, num)


def _parse_pre(ident: str) -> tuple[str, int]:
    if not ident[0].isalpha():
        raise ValueError(f"pre-release must start with a/b/alpha/beta/rc, got {ident!r}")
    split = 0
    while split < len(ident) and ident[split].isalpha():
        split += 1
    tag = ident[:split].lower()
    if tag not in _PRE_RANK:
        raise ValueError(f"unsupported pre-release tag {tag!r}")
    num_text = ident[split:]
    if not num_text.isdigit():
        raise ValueError(f"pre-release must end with a number, got {ident!r}")
    return tag, int(num_text)
