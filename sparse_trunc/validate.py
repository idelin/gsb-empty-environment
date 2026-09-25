"""Validation by enumerating input-box corner points on small chains.

Corner checks verify that the issued certificate is an upper bound on the
true deviation; they never replace the bound with an observed error.
"""

from __future__ import annotations

from itertools import product
from typing import List, Sequence, Tuple

from .core import TruncatedChain


class CornerCheck:
    def __init__(self, points: int, max_observed_linf: float,
                 bound_linf: float, bound_holds: bool,
                 observed_examples: List[Tuple[List[float], float]]) -> None:
        self.points = points
        self.max_observed_linf = max_observed_linf
        self.bound_linf = bound_linf
        self.bound_holds = bound_holds
        self.observed_examples = observed_examples

    def render(self) -> str:
        status = "HOLDS" if self.bound_holds else "VIOLATED"
        lines = [
            f"corner enumeration over {self.points} box corners",
            f"  max observed |error|_inf : {self.max_observed_linf:.6g}",
            f"  certificate bound        : {self.bound_linf:.6g}",
            f"  bound {status} (observed is never substituted for the bound)",
        ]
        return "\n".join(lines)


def _corners(lo: Sequence[float], hi: Sequence[float]):
    for choice in product((0, 1), repeat=len(lo)):
        yield [hi[j] if choice[j] else lo[j] for j in range(len(lo))]


def enumerate_corners(chain: TruncatedChain,
                      max_corners: int = 1 << 16) -> List[List[float]]:
    dim = len(chain.input_lo)
    if 1 << dim > max_corners:
        raise ValueError(
            f"refusing to enumerate {1 << dim} corners; raise max_corners")
    return [p for p in _corners(chain.input_lo, chain.input_hi)]


def check_corners(chain: TruncatedChain, bound_linf: float,
                  tol: float = 1e-9,
                  max_corners: int = 1 << 16,
                  keep_examples: int = 3) -> CornerCheck:
    points = enumerate_corners(chain, max_corners)
    exact = chain.exact_outputs(points)
    approx = chain.approximate_outputs(points)
    worst = 0.0
    examples: List[Tuple[List[float], float]] = []
    for p, e_out, a_out in zip(points, exact, approx):
        dev = max(abs(a - b) for a, b in zip(e_out, a_out))
        if dev > worst:
            worst = dev
        if len(examples) < keep_examples:
            examples.append((list(p), dev))
    holds = worst <= bound_linf + tol * max(1.0, abs(bound_linf))
    return CornerCheck(len(points), worst, bound_linf, holds, examples)
