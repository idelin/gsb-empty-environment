"""语义化版本解析、比较与约束匹配（仅标准库）。

支持：major.minor.patch[-prerelease][+build]
比较规则遵循 SemVer 2.0.0：预发布版本低于同版本号的正式版。
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering
from typing import Sequence

_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<pre>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


class VersionError(ValueError):
    """版本字符串或约束不合法。"""


def _pre_key(identifier: str):
    if identifier.isdigit():
        # 纯数字按数值比较，且数字标识符小于字符串标识符
        return (0, int(identifier))
    return (1, identifier)


@total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @staticmethod
    def parse(text: str) -> "Version":
        if not isinstance(text, str):
            raise VersionError(f"版本必须是字符串：{text!r}")
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise VersionError(f"无法解析版本：{text!r}")
        pre = tuple(match.group("pre").split(".")) if match.group("pre") else ()
        return Version(
            major=int(match.group("major")),
            minor=int(match.group("minor")),
            patch=int(match.group("patch")),
            prerelease=pre,
        )

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    @property
    def core(self) -> tuple[int, int, int]:
        return (self.major, self.minor, self.patch)

    def _sort_key(self):
        # 无预发布：(1, ) 高于任意预发布 (0, ...)
        if self.prerelease:
            return (0, tuple(_pre_key(p) for p in self.prerelease))
        return (1, ())

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return self.core == other.core and self.prerelease == other.prerelease

    def __lt__(self, other: "Version") -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        if self.core != other.core:
            return self.core < other.core
        if not self.prerelease and not other.prerelease:
            return False
        if not self.prerelease:
            return False
        if not other.prerelease:
            return True
        return self._sort_key()[1] < other._sort_key()[1]

    def __str__(self) -> str:
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            base += "-" + ".".join(self.prerelease)
        return base


# ---------------------------------------------------------------- 约束


class Constraint:
    """单个版本约束，如 ^1.2.3、~2.0、>=1.0.0-rc.1、=1.2.3。"""

    _OPS = ("^", "~", ">=", "<=", "==", "!=", ">", "<", "=")

    def __init__(self, op: str, base: Version, raw: str):
        self.op = op
        self.base = base
        self.raw = raw

    @staticmethod
    def parse(text: str) -> "Constraint":
        raw = text.strip()
        op = "="
        body = raw
        for candidate in Constraint._OPS:
            if raw.startswith(candidate):
                op = "=" if candidate == "==" else candidate
                body = raw[len(candidate):].strip()
                break
        # 裸版本号视为精确匹配；补齐三段
        version = _parse_partial(body)
        return Constraint(op, version, raw)

    # ------------------------------------------------------------ 匹配
    def satisfies(self, version: Version) -> bool:
        op, base = self.op, self.base
        if op == "=":
            return version == base
        if op == "!=":
            return version != base
        if op == ">":
            return version > base
        if op == "<":
            return version < base
        if op == ">=":
            return version >= base
        if op == "<=":
            return version <= base
        if op == "^":
            return _caret_ok(version, base)
        if op == "~":
            return _tilde_ok(version, base)
        raise VersionError(f"未知约束运算符：{op}")

    def references_prerelease(self) -> bool:
        return self.base.is_prerelease

    def __str__(self) -> str:
        return self.raw


def _parse_partial(body: str) -> Version:
    core_text, sep, suffix = body.partition("-")
    parts = core_text.split(".")
    if not (1 <= len(parts) <= 3):
        raise VersionError(f"无法解析版本约束：{body!r}")
    parts += ["0"] * (3 - len(parts))
    return Version.parse(".".join(parts) + (f"-{suffix}" if sep else ""))


def _caret_ok(v: Version, base: Version) -> bool:
    # ^0.0.x 仅允许 x；^0.y.z（y>0）允许 0.y.*；其余允许同一 major
    if v < base:
        return False
    if base.major > 0:
        return v.major == base.major
    if base.minor > 0:
        return v.major == 0 and v.minor == base.minor
    return v.major == 0 and v.minor == 0 and v.patch == base.patch


def _tilde_ok(v: Version, base: Version) -> bool:
    # ~major.minor.patch 允许同 minor 的更高 patch；~major.minor 等价
    if v < base:
        return False
    return v.major == base.major and v.minor == base.minor


def parse_constraints(text: str) -> list[Constraint]:
    """逗号分隔的约束列表，空字符串表示无约束。"""
    if text is None or not str(text).strip():
        return []
    return [Constraint.parse(part) for part in str(text).split(",") if part.strip()]


def satisfies_all(version: Version, constraints: Sequence[Constraint]) -> bool:
    # 预发布门控：候选是预发布时，至少有一个约束显式引用预发布
    if version.is_prerelease and not any(c.references_prerelease() for c in constraints):
        return False
    return all(c.satisfies(version) for c in constraints)
