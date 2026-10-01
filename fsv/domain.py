"""Explicit bounded integer domain with two selectable overflow semantics."""

from dataclasses import dataclass

from .faults import FaultError

WRAP = "wrap"    # modular arithmetic: values wrap into [lo, hi]
ERROR = "error"  # strict: overflowing an operation raises an overflow fault


@dataclass(frozen=True)
class IntDomain:
    """All integers in a program run live inside [lo, hi] (inclusive)."""

    lo: int
    hi: int
    mode: str = ERROR

    def __post_init__(self):
        if self.lo > self.hi:
            raise ValueError("empty integer domain")
        if self.mode not in (WRAP, ERROR):
            raise ValueError(f"unknown overflow mode: {self.mode}")

    @property
    def span(self):
        return self.hi - self.lo + 1

    def check(self, value, loc):
        """Bring `value` into the domain according to the overflow mode."""
        if self.lo <= value <= self.hi:
            return value
        if self.mode == WRAP:
            return (value - self.lo) % self.span + self.lo
        raise FaultError(
            "overflow", f"value {value} outside [{self.lo}, {self.hi}]", loc
        )

    def __str__(self):
        return f"int[{self.lo}..{self.hi}]/{self.mode}"
