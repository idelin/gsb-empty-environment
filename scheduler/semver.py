"""major.minor.patch 版本校验，支持预发布与构建元数据，不联网。"""

from __future__ import annotations

import re
from dataclasses import dataclass

_SEMVER_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


def is_valid_version(text: str) -> bool:
    return isinstance(text, str) and _SEMVER_RE.match(text) is not None


@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base

    def sort_key(self):
        # 正式版高于同号预发布版；预发布标识符按 semver 规则比较。
        if not self.prerelease:
            return (self.major, self.minor, self.patch, 1, ())
        return (
            self.major,
            self.minor,
            self.patch,
            0,
            tuple(_identifier_key(p) for p in self.prerelease),
        )


def _identifier_key(ident: str):
    if ident.isdigit():
        return (0, int(ident), "")
    return (1, 0, ident)


def parse_version(text: str) -> Version:
    match = _SEMVER_RE.match(text) if isinstance(text, str) else None
    if match is None:
        raise ValueError(f"非法版本号（需为 major.minor.patch，可带预发布）: {text!r}")
    major, minor, patch = (int(g) for g in match.group(1, 2, 3))
    prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
    return Version(major, minor, patch, prerelease)
