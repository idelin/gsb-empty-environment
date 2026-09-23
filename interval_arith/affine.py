"""Affine arithmetic for expressions with repeated variable references.

An affine form is

    value = const + sum_s coeffs[s] * e_s  +  remainder

where each ``e_s`` is an independent noise symbol in [-1, 1] and
``remainder`` is a conservative interval enclosing every nonlinear term.

* The linear part (const + coeffs) is carried *exactly* (Fractions), so
  correlations between repeated references to the same variable cancel
  exactly:  x - x == 0, and x * x on [-1, 1] ranges over exactly [0, 1].
* Every nonlinear term is bounded conservatively and verifiably:
  e_s^2 lies in [0, 1] (exact), cross terms e_s * e_t in [-1, 1], and
  remainders compose through exact interval arithmetic.
"""

from __future__ import annotations

from fractions import Fraction

from .core import Interval, ZERO
from .certificate import Certificate, Step


class AffineForm:
    __slots__ = ("const", "coeffs", "remainder")

    def __init__(self, const=ZERO, coeffs=None, remainder=None):
        self.const = Fraction(const)
        self.coeffs = {s: Fraction(c) for s, c in (coeffs or {}).items()
                       if Fraction(c) != 0}
        self.remainder = remainder if remainder is not None else Interval.point(ZERO)

    # ---------------------------------------------------------- constructors
    @classmethod
    def constant(cls, value, cert: Certificate | None = None) -> "AffineForm":
        form = cls(Fraction(value))
        if cert is not None:
            cert.add(Step("const",
                          f"constant {value}; valid on the whole domain",
                          (Fraction(value),), form))
        return form

    @classmethod
    def from_interval(cls, symbol: str, iv: Interval,
                      cert: Certificate | None = None) -> "AffineForm":
        """Reparametrize a bounded interval as center + radius * e_symbol."""
        if not iv.is_bounded():
            raise ValueError("affine noise symbols require a bounded interval")
        center = (iv.lo + iv.hi) / 2
        radius = (iv.hi - iv.lo) / 2
        form = cls(center, {symbol: radius})
        if cert is not None:
            cert.add(Step("introduce",
                          f"introduce noise symbol {symbol} in [-1, 1]; exact "
                          f"reparametrization of {iv}",
                          (symbol, iv), form))
        return form

    # ------------------------------------------------------------- queries
    def linear_range(self) -> Interval:
        rad = sum((abs(c) for c in self.coeffs.values()), ZERO)
        return Interval(self.const - rad, self.const + rad)

    def range(self) -> Interval:
        """Certified enclosure: exact linear radius + conservative remainder."""
        return self.linear_range() + self.remainder

    def __eq__(self, other):
        return (isinstance(other, AffineForm)
                and self.const == other.const
                and self.coeffs == other.coeffs
                and self.remainder == other.remainder)

    def __repr__(self):
        terms = " + ".join(f"{c}*{s}" for s, c in sorted(self.coeffs.items()))
        base = f"{self.const}" + (f" + {terms}" if terms else "")
        if self.remainder != Interval.point(ZERO):
            base += f" + r{self.remainder}"
        return f"AffineForm({base})"


# ----------------------------------------------------------------- operations
def affine_add(f: AffineForm, g: AffineForm,
               cert: Certificate | None = None) -> AffineForm:
    coeffs = dict(f.coeffs)
    for s, c in g.coeffs.items():
        coeffs[s] = coeffs.get(s, ZERO) + c
    out = AffineForm(f.const + g.const, coeffs, f.remainder + g.remainder)
    if cert is not None:
        cert.add(Step("affine_add",
                      "addition of affine forms: linear part exact, remainders "
                      "added exactly; valid on the whole domain", (f, g), out))
    return out


def affine_neg(f: AffineForm, cert: Certificate | None = None) -> AffineForm:
    out = AffineForm(-f.const, {s: -c for s, c in f.coeffs.items()},
                     -f.remainder)
    if cert is not None:
        cert.add(Step("affine_neg", "negation: exact on the whole domain",
                      (f,), out))
    return out


def affine_sub(f: AffineForm, g: AffineForm,
               cert: Certificate | None = None) -> AffineForm:
    coeffs = dict(f.coeffs)
    for s, c in g.coeffs.items():
        coeffs[s] = coeffs.get(s, ZERO) - c
    out = AffineForm(f.const - g.const, coeffs, f.remainder - g.remainder)
    if cert is not None:
        cert.add(Step("affine_sub",
                      "subtraction of affine forms: shared noise symbols cancel "
                      "exactly; valid on the whole domain", (f, g), out))
    return out


def affine_scale(f: AffineForm, k,
                 cert: Certificate | None = None) -> AffineForm:
    k = Fraction(k)
    out = AffineForm(f.const * k, {s: c * k for s, c in f.coeffs.items()},
                     f.remainder * Interval.point(k))
    if cert is not None:
        cert.add(Step("affine_scale",
                      f"scaling by exact rational {k}; valid on the whole domain",
                      (f, k), out))
    return out


def affine_mul(f: AffineForm, g: AffineForm,
               cert: Certificate | None = None) -> AffineForm:
    const = f.const * g.const
    coeffs = {}
    for s, c in f.coeffs.items():
        coeffs[s] = coeffs.get(s, ZERO) + c * g.const
    for s, c in g.coeffs.items():
        coeffs[s] = coeffs.get(s, ZERO) + c * f.const

    # Nonlinear remainder, bounded conservatively but symbol-aware:
    #   (sum a_s e_s)(sum b_t e_t)
    #       = sum_s a_s b_s e_s^2  +  sum_{s<t} (a_s b_t + a_t b_s) e_s e_t
    # with e_s^2 in [0, 1] (exact) and e_s e_t in [-1, 1].
    qlo = qhi = ZERO
    symbols = sorted(set(f.coeffs) | set(g.coeffs))
    for s in symbols:
        ab = f.coeffs.get(s, ZERO) * g.coeffs.get(s, ZERO)
        qlo += min(ab, ZERO)
        qhi += max(ab, ZERO)
    for i, s in enumerate(symbols):
        for t in symbols[i + 1:]:
            c = (f.coeffs.get(s, ZERO) * g.coeffs.get(t, ZERO)
                 + f.coeffs.get(t, ZERO) * g.coeffs.get(s, ZERO))
            qlo -= abs(c)
            qhi += abs(c)

    rem = f.remainder * g.remainder
    rem = rem + f.remainder * g.linear_range()
    rem = rem + g.remainder * f.linear_range()
    rem = rem + Interval(qlo, qhi)

    out = AffineForm(const, coeffs, rem)
    if cert is not None:
        cert.add(Step("affine_mul",
                      "linear part exact; e_s^2 bounded in [0, 1], cross terms "
                      "e_s*e_t in [-1, 1], remainders composed by exact "
                      "interval arithmetic; valid for all noise values in [-1, 1]",
                      (f, g), out))
    return out
