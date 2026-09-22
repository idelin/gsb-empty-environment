from __future__ import annotations

from dataclasses import dataclass
import re


_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<pre>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


@dataclass(frozen=True, slots=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str | int, ...] = ()
    build: str = ""

    @classmethod
    def parse(cls, raw: str | int | float) -> "Version":
        text = str(raw).strip()
        match = _VERSION_RE.fullmatch(text)
        if not match:
            raise ValueError(f"invalid semantic version: {raw!r}")

        raw_prerelease_parts = (match.group("pre") or "").split(".") if match.group("pre") else []
        prerelease = tuple(
            int(part) if part.isdigit() else part
            for part in raw_prerelease_parts
        )
        if any(isinstance(part, int) and len(str(part)) != len(raw_part)
               for part, raw_part in zip(prerelease, raw_prerelease_parts)):
            raise ValueError(f"numeric prerelease identifiers cannot have leading zeroes: {raw!r}")
        return cls(
            int(match.group("major")),
            int(match.group("minor")),
            int(match.group("patch")),
            prerelease,
            match.group("build") or "",
        )

    def __str__(self) -> str:
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(str(part) for part in self.prerelease)
        if self.build:
            text += "+" + self.build
        return text

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    @staticmethod
    def _cmp_prerelease(left: tuple[str | int, ...],
                        right: tuple[str | int, ...]) -> int:
        for left_part, right_part in zip(left, right):
            if left_part == right_part:
                continue
            if isinstance(left_part, int) and isinstance(right_part, int):
                return -1 if left_part < right_part else 1
            if isinstance(left_part, int):
                return -1
            if isinstance(right_part, int):
                return 1
            return -1 if left_part < right_part else 1
        if len(left) == len(right):
            return 0
        return -1 if len(left) < len(right) else 1

    def _cmp(self, other: "Version") -> int:
        core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if core < other_core:
            return -1
        if core > other_core:
            return 1
        if not self.prerelease and not other.prerelease:
            return 0
        if not self.prerelease:
            return 1
        if not other.prerelease:
            return -1
        return self._cmp_prerelease(self.prerelease, other.prerelease)

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, Version)
            and (self.major, self.minor, self.patch, self.prerelease)
            == (other.major, other.minor, other.patch, other.prerelease)
        )

    def __hash__(self) -> int:
        return hash((self.major, self.minor, self.patch, self.prerelease))

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp(other) < 0

    def __le__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp(other) <= 0

    def __gt__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp(other) > 0

    def __ge__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self._cmp(other) >= 0


@dataclass(frozen=True, slots=True)
class Constraint:
    operator: str
    version: Version | None = None
    raw: str = "*"

    @classmethod
    def parse(cls, raw: object) -> "Constraint":
        if raw is None:
            return cls("*", None, "*")
        text = str(raw).strip()
        if text in {"", "*", "x", "X", "latest", "any"}:
            return cls("*", None, text)
        for operator in (">=", "<=", "==", "!=", ">", "<", "~", "^", "="):
            if text.startswith(operator):
                value = text[len(operator):].strip()
                normalized_operator = "==" if operator == "=" else operator
                return cls(normalized_operator, Version.parse(value), text)
        return cls("==", Version.parse(text), text)

    def allows(self, candidate: Version) -> bool:
        if self.operator == "*":
            return True
        assert self.version is not None
        if self.operator == "==":
            if candidate.build or self.version.build:
                return candidate == self.version
            return (
                candidate.major == self.version.major
                and candidate.minor == self.version.minor
                and candidate.patch == self.version.patch
                and candidate.prerelease == self.version.prerelease
            )
        if self.operator == "!=":
            return not (
                candidate.major == self.version.major
                and candidate.minor == self.version.minor
                and candidate.patch == self.version.patch
                and candidate.prerelease == self.version.prerelease
                and candidate.build == self.version.build
            )
        if self.operator == ">=":
            return candidate >= self.version
        if self.operator == "<=":
            return candidate <= self.version
        if self.operator == ">":
            return candidate > self.version
        if self.operator == "<":
            return candidate < self.version
        if self.operator == "~":
            if self.version.prerelease:
                lower = self.version
                upper = Version(self.version.major, self.version.minor,
                                self.version.patch + 1)
            else:
                lower = Version(self.version.major, self.version.minor, 0)
                upper = Version(self.version.major, self.version.minor + 1, 0)
            return lower <= candidate < upper
        if self.operator == "^":
            lower = self.version
            if self.version.major > 0:
                upper = Version(self.version.major + 1, 0, 0)
            elif self.version.minor > 0:
                upper = Version(0, self.version.minor + 1, 0)
            else:
                upper = Version(0, 0, self.version.patch + 1)
            return lower <= candidate < upper
        raise ValueError(f"unsupported constraint operator: {self.operator}")

    @property
    def explicitly_mentions_prerelease(self) -> bool:
        return self.version is not None and self.version.is_prerelease


def parse_constraints(value: object) -> tuple[Constraint, ...]:
    if isinstance(value, (list, tuple)):
        if not value:
            return (Constraint.parse(None),)
        return tuple(Constraint.parse(item) for item in value)
    return (Constraint.parse(value),)
