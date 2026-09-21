"""SemVer (major.minor.patch[-prerelease][+build]) parsing and ordering.

Only the subset needed by the offline locker is implemented:
- versions MUST be exactly ``major.minor.patch`` with numeric, non-negative
  components;
- an optional dot-separated prerelease identifier may follow ``-``;
- build metadata (``+...``) is accepted but ignored for precedence.

Prerelease precedence follows SemVer 2.0: numeric identifiers compare
numerically, alphanumeric identifiers compare lexically, numeric identifiers
rank lower than alphanumeric ones, and a longer prerelease list wins when all
preceding identifiers are equal. A version WITHOUT a prerelease outranks the
same version WITH one.
"""

from __future__ import annotations

from dataclasses import dataclass


class VersionError(ValueError):
    """Raised when a string is not a valid major.minor.patch version."""


@dataclass(frozen=True, order=True)
class Version:
    """A parsed semantic version with ordering."""

    major: int
    minor: int
    patch: int
    # prerelease parts, each part int (numeric) or str (alphanumeric);
    # an empty tuple means "no prerelease" and ranks HIGHEST.
    prerelease: tuple[object, ...] = ()
    build: str = ""

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    @property
    def core(self) -> str:
        return f"{self.major}.{self.minor}.{self.patch}"

    def same_core(self, other: "Version") -> bool:
        return (
            self.major == other.major
            and self.minor == other.minor
            and self.patch == other.patch
        )

    def __str__(self) -> str:
        text = self.core
        if self.prerelease:
            text += "-" + ".".join(str(part) for part in self.prerelease)
        if self.build:
            text += "+" + self.build
        return text


def _parse_prerelease(text: str) -> tuple[object, ...]:
    parts: list[object] = []
    for identifier in text.split("."):
        if not identifier:
            raise VersionError(f"empty prerelease identifier in {text!r}")
        if identifier.isdigit():
            # SemVer forbids leading zeroes in numeric identifiers.
            if len(identifier) > 1 and identifier[0] == "0":
                raise VersionError(f"leading zero in numeric identifier {identifier!r}")
            parts.append(int(identifier))
        else:
            if not all(ch.isalnum() or ch == "-" for ch in identifier):
                raise VersionError(f"illegal character in prerelease identifier {identifier!r}")
            parts.append(identifier)
    return tuple(parts)


def parse_version(text: str) -> Version:
    """Parse ``major.minor.patch[-pre][+build]`` into a :class:`Version`."""
    if not isinstance(text, str):
        raise VersionError(f"version must be a string, got {type(text).__name__}")
    raw = text.strip()
    build = ""
    if "+" in raw:
        raw, build = raw.split("+", 1)
        if not build:
            raise VersionError("empty build metadata")
    prerelease: tuple[object, ...] = ()
    if "-" in raw:
        core, pre = raw.split("-", 1)
        if not pre:
            raise VersionError("empty prerelease")
        prerelease = _parse_prerelease(pre)
    else:
        core = raw
    numbers = core.split(".")
    if len(numbers) != 3:
        raise VersionError(f"expected major.minor.patch, got {text!r}")
    try:
        major, minor, patch = (int(n) for n in numbers)
    except ValueError as exc:
        raise VersionError(f"non-numeric version component in {text!r}") from exc
    for value, name in ((major, "major"), (minor, "minor"), (patch, "patch")):
        if value < 0:
            raise VersionError(f"{name} version component must be non-negative")
    return Version(major, minor, patch, prerelease, build)


def _pre_rank(parts: tuple[object, ...]) -> tuple:
    # Represent prerelease as a sortable tuple: absence of prerelease is
    # encoded via a leading 1 (ranks above), presence via a leading 0.
    if not parts:
        return (1,)
    rank: list[tuple] = [(0,)]
    for part in parts:
        if isinstance(part, int):
            rank.append((0, part, ""))
        else:
            rank.append((1, 0, part))
    # equal up to the length we have: the longer prerelease is higher, which
    # tuple comparison handles by appending nothing (pad with "more" marker).
    rank.append((2,))
    return tuple(rank)


def compare_versions(a: Version, b: Version) -> int:
    """Return -1/0/1 for precedence comparison of two versions."""
    core_a = (a.major, a.minor, a.patch)
    core_b = (b.major, b.minor, b.patch)
    if core_a != core_b:
        return -1 if core_a < core_b else 1
    return _compare_pre(a.prerelease, b.prerelease)


def _compare_pre(a: tuple[object, ...], b: tuple[object, ...]) -> int:
    if not a and not b:
        return 0
    if not a:
        return 1
    if not b:
        return -1
    for x, y in zip(a, b):
        if type(x) is int and type(y) is int:
            if x != y:
                return -1 if x < y else 1
        elif type(x) is str and type(y) is str:
            if x != y:
                return -1 if x < y else 1
        else:
            # numeric always ranks lower than alphanumeric
            if type(x) is int:
                return -1
            return 1
    if len(a) == len(b):
        return 0
    return -1 if len(a) < len(b) else 1


# Monkeypatch ordering onto Version (kept here so the dataclass definition
# above stays free of custom comparison boilerplate).
Version.__lt__ = lambda self, other: compare_versions(self, other) < 0  # type: ignore[assignment]
Version.__le__ = lambda self, other: compare_versions(self, other) <= 0  # type: ignore[assignment]
Version.__gt__ = lambda self, other: compare_versions(self, other) > 0  # type: ignore[assignment]
Version.__ge__ = lambda self, other: compare_versions(self, other) >= 0  # type: ignore[assignment]
Version.__eq__ = lambda self, other: (  # type: ignore[assignment]
    isinstance(other, Version) and compare_versions(self, other) == 0
)
Version.__hash__ = lambda self: hash(  # type: ignore[assignment]
    (self.major, self.minor, self.patch, tuple(self.prerelease))
)
