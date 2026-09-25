"""Diagnostics. Positions are half-open UTF-8 byte ranges into module source."""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    """Half-open byte interval [start, end); byte offsets are UTF-8 based."""

    start: int
    end: int

    def to_json(self) -> list[int]:
        return [self.start, self.end]

    @staticmethod
    def from_json(data) -> "Span":
        return Span(int(data[0]), int(data[1]))


@dataclass
class Diagnostic:
    module: str
    span: Span | None
    code: str
    message: str
    chain: tuple[tuple[str, Span | None], ...] = ()

    def format(self) -> str:
        loc = f"[{self.span.start},{self.span.end})" if self.span else "[-]"
        out = f"{self.module}:{loc}: {self.code}: {self.message}"
        for label, sp in self.chain:
            loc2 = f"[{sp.start},{sp.end})" if sp else "[-]"
            out += f"\n    via {label} {loc2}"
        return out

    def to_json(self) -> dict:
        return {
            "module": self.module,
            "span": self.span.to_json() if self.span else None,
            "code": self.code,
            "message": self.message,
            "chain": [[label, sp.to_json() if sp else None] for label, sp in self.chain],
        }

    @staticmethod
    def from_json(data: dict) -> "Diagnostic":
        return Diagnostic(
            data["module"],
            Span.from_json(data["span"]) if data["span"] else None,
            data["code"],
            data["message"],
            tuple(
                (label, Span.from_json(sp) if sp else None)
                for label, sp in data["chain"]
            ),
        )
