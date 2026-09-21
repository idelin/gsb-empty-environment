"""Debug tracer: records the chain of thunk forcings.

When enabled, produces indented lines such as:

    -> force x  (bytes 24..25)
      -> force y  (bytes 40..41)
      <- y = 2
    <- x = 3
    ~~ x  (cached)

The tracer also owns the stack of thunks currently being forced, which
the interpreter uses to reconstruct the cycle when a black hole is hit.
"""

from __future__ import annotations


class Tracer:
    def __init__(self, enabled: bool = False) -> None:
        self.enabled = enabled
        self.stack: list = []  # thunks currently being forced (innermost last)
        self.lines: list[str] = []

    def _emit(self, depth: int, msg: str) -> None:
        if self.enabled:
            self.lines.append("  " * depth + msg)

    def enter(self, thunk) -> None:
        s, e = thunk.name_span
        self._emit(len(self.stack), f"-> force {thunk.name}  (bytes {s}..{e})")
        self.stack.append(thunk)

    def produced(self, thunk, value: int) -> None:
        self._emit(len(self.stack) - 1, f"<- {thunk.name} = {value}")

    def raised(self, thunk, exc: Exception) -> None:
        self._emit(len(self.stack) - 1, f"!! {thunk.name} raised {type(exc).__name__}: {exc}")

    def exit(self, thunk) -> None:
        self.stack.pop()

    def cached(self, thunk) -> None:
        self._emit(len(self.stack), f"~~ {thunk.name}  (cached value {thunk.value})")

    def cached_error(self, thunk) -> None:
        self._emit(
            len(self.stack),
            f"~~ {thunk.name}  (cached error {type(thunk.error).__name__})",
        )

    def cycle_from(self, name: str) -> list[str]:
        """Forcing chain from the first in-progress occurrence of ``name``."""
        names = [t.name for t in self.stack]
        try:
            start = names.index(name)
        except ValueError:
            start = 0
        return names[start:] + [name]
