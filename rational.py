"""Exact univariate rational-function simplifier over Q.

No symbolic-algebra library is used.  Everything is computed exactly with
fractions.Fraction; only the standard library is imported.

Provides:
* Polynomial -- dense polynomials over Q with exact gcd, square-free
  decomposition (Yun's algorithm) and real-root isolation (Sturm's
  theorem): rational roots come back as Fractions, irrational roots as
  disjoint rational isolating intervals.
* IsolatedRoot / RationalPoint -- points of the real exclusion set.
* RationalFunction -- a fraction p/q of polynomials reduced by the
  polynomial gcd while retaining the ORIGINAL domain as a real-root
  exclusion set, so removable singularities are never lost.

Every transformed RationalFunction records the domain each step is valid
on in its ``steps`` list.
"""

from __future__ import annotations

from fractions import Fraction
from math import floor, gcd
from typing import Iterable, List, Tuple, Union  # noqa: F401

Number = Union[int, Fraction]


class ZeroDenominatorError(ValueError):
    """Raised when a rational function with a zero denominator is formed."""


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _divisors(n: int) -> List[int]:
    n = abs(n)
    if n == 0:
        return [1]
    result = {1}
    factor = 2
    while factor * factor <= n:
        while n % factor == 0:
            result |= {d * factor for d in list(result)}
            n //= factor
        factor += 1 if factor == 2 else 2
    if n > 1:
        result |= {d * n for d in list(result)}
    return sorted(result)


def _fraction_str(value: Fraction) -> str:
    if value.denominator == 1:
        return str(value.numerator)
    return f"({value.numerator}/{value.denominator})"


# ---------------------------------------------------------------------------
# Polynomial over Q
# ---------------------------------------------------------------------------


class Polynomial:
    """Dense univariate polynomial with Fraction coefficients.

    Coefficients are stored by ascending degree with trailing zeros
    stripped, giving canonical equality/hashing.
    """

    __slots__ = ("coeffs",)

    def __init__(self, coeffs: Iterable[Number] = ()) -> None:
        raw = [Fraction(c) for c in coeffs]
        while len(raw) > 1 and raw[-1] == 0:
            raw.pop()
        if not raw:
            raw = [Fraction(0)]
        self.coeffs: Tuple[Fraction, ...] = tuple(raw)

    @property
    def degree(self) -> int:
        return len(self.coeffs) - 1

    @property
    def lc(self) -> Fraction:
        return self.coeffs[-1]

    def is_zero(self) -> bool:
        return self.degree == 0 and self.coeffs[0] == 0

    def is_constant(self) -> bool:
        return self.degree == 0

    def copy(self) -> "Polynomial":
        return Polynomial(self.coeffs)

    def __call__(self, x: Number) -> Fraction:
        result = Fraction(0)
        for coeff in reversed(self.coeffs):
            result = result * x + coeff
        return result

    def _coerce(self, other: Union["Polynomial", Number]) -> "Polynomial":
        if isinstance(other, Polynomial):
            return other
        return Polynomial([Fraction(other)])

    def __add__(self, other: Union["Polynomial", Number]) -> "Polynomial":
        other = self._coerce(other)
        n = max(len(self.coeffs), len(other.coeffs))
        a = self.coeffs + (Fraction(0),) * (n - len(self.coeffs))
        b = other.coeffs + (Fraction(0),) * (n - len(other.coeffs))
        return Polynomial(tuple(x + y for x, y in zip(a, b)))

    __radd__ = __add__

    def __neg__(self) -> "Polynomial":
        return Polynomial(tuple(-c for c in self.coeffs))

    def __sub__(self, other: Union["Polynomial", Number]) -> "Polynomial":
        return self + (-self._coerce(other))

    def __rsub__(self, other: Number) -> "Polynomial":
        return (-self) + other

    def __mul__(self, other: Union["Polynomial", Number]) -> "Polynomial":
        other = self._coerce(other)
        if self.is_zero() or other.is_zero():
            return Polynomial([0])
        out = [Fraction(0)] * (self.degree + other.degree + 1)
        for i, ci in enumerate(self.coeffs):
            if ci == 0:
                continue
            for j, cj in enumerate(other.coeffs):
                out[i + j] += ci * cj
        return Polynomial(out)

    __rmul__ = __mul__

    def __pow__(self, exponent: int) -> "Polynomial":
        if exponent < 0:
            raise ValueError("polynomial powers must be non-negative")
        result = Polynomial([1])
        base = self
        while exponent:
            if exponent & 1:
                result = result * base
            base = base * base
            exponent >>= 1
        return result

    def scale(self, scalar: Number) -> "Polynomial":
        scalar = Fraction(scalar)
        if scalar == 0:
            return Polynomial([0])
        return Polynomial(tuple(c * scalar for c in self.coeffs))

    def divmod(
        self, divisor: "Polynomial"
    ) -> Tuple["Polynomial", "Polynomial"]:
        """Exact polynomial long division: (quotient, remainder)."""
        if divisor.is_zero():
            raise ZeroDivisionError("polynomial division by zero")
        remainder = list(self.coeffs)
        dcoeffs = list(divisor.coeffs)
        quotient = [Fraction(0)] * max(0, self.degree - divisor.degree + 1)
        for k in range(self.degree - divisor.degree, -1, -1):
            factor = remainder[k + divisor.degree] / divisor.lc
            quotient[k] = factor
            for j in range(divisor.degree + 1):
                remainder[k + j] -= factor * dcoeffs[j]
        return Polynomial(quotient), Polynomial(remainder)

    def __floordiv__(self, divisor: "Polynomial") -> "Polynomial":
        return self.divmod(divisor)[0]

    def __mod__(self, divisor: "Polynomial") -> "Polynomial":
        return self.divmod(divisor)[1]

    def __divmod__(
        self, divisor: "Polynomial"
    ) -> Tuple["Polynomial", "Polynomial"]:
        return self.divmod(divisor)

    def derivative(self) -> "Polynomial":
        if self.degree == 0:
            return Polynomial([0])
        return Polynomial(
            tuple(i * self.coeffs[i] for i in range(1, len(self.coeffs)))
        )

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Polynomial):
            return NotImplemented
        return self.coeffs == other.coeffs

    def __hash__(self) -> int:
        return hash(self.coeffs)

    # -- normalisation ----------------------------------------------------

    def monic(self) -> "Polynomial":
        if self.is_zero():
            return Polynomial([0])
        return self.scale(Fraction(1) / self.lc)

    def to_integer(self) -> "Polynomial":
        """Primitive integer coefficients with positive leading sign."""
        nonzero = [c for c in self.coeffs if c != 0]
        if not nonzero:
            return Polynomial([0])
        common = nonzero[0].denominator
        for frac in nonzero[1:]:
            common = common * frac.denominator // gcd(
                common, frac.denominator
            )
        ints = [int(c * common) for c in self.coeffs]
        content = 0
        for value in ints:
            content = gcd(content, abs(value))
        if content:
            ints = [value // content for value in ints]
        if ints[-1] < 0:
            ints = [-v for v in ints]
        return Polynomial(ints)

    @staticmethod
    def gcd(a: "Polynomial", b: "Polynomial") -> "Polynomial":
        """Monic polynomial gcd over Q via the exact Euclidean algorithm."""
        if a.is_zero() and b.is_zero():
            return Polynomial([0])
        r0, r1 = a, b
        while not r1.is_zero():
            r0, r1 = r1, r0 % r1
        return r0.monic()

    # -- square-free decomposition (Yun) -----------------------------------

    def square_free(self) -> List[Tuple[int, "Polynomial"]]:
        """Yun's square-free decomposition over Q.

        Returns ``[(multiplicity, monic factor), ...]`` ordered by
        multiplicity; product of factor**multiplicity equals self (self is
        first made monic, so constants return []).
        """
        if self.is_constant():
            return []
        f = self.to_integer().monic()
        fp = f.derivative()
        c = Polynomial.gcd(f, fp)
        w = f // c
        z = fp // c
        factors: List[Tuple[int, Polynomial]] = []
        index = 1
        while not w.is_constant():
            y = Polynomial.gcd(w, z - w.derivative())
            z = (z - w.derivative()) // y
            w = w // y
            if not y.is_constant():
                factors.append((index, y))
            index += 1
        return factors

    # -- rational roots ----------------------------------------------------

    def rational_roots(self) -> List[Fraction]:
        """All distinct rational roots (rational-root theorem, exact)."""
        poly = self.to_integer()
        if poly.is_constant():
            return []
        if poly(0) == 0:
            stripped = poly
            while stripped(0) == 0:
                stripped = stripped // Polynomial([0, 1])
            return sorted({Fraction(0), *stripped.rational_roots()})
        p_values = _divisors(int(poly.coeffs[0]))
        q_values = _divisors(int(poly.lc))
        candidates = set()
        for p in p_values:
            for q in q_values:
                candidates.add(Fraction(p, q))
                candidates.add(Fraction(-p, q))
        return sorted(r for r in candidates if poly(r) == 0)

    # -- Sturm chains and real-root isolation ------------------------------

    def sturm_chain(self) -> List["Polynomial"]:
        """Sturm sequence [p, p', -(p mod p'), ...] with exact signs."""
        if self.is_constant():
            return [self.copy()]
        chain = [self.copy(), self.derivative()]
        while not chain[-1].is_zero():
            chain.append(-(chain[-2] % chain[-1]))
        chain.pop()  # trailing zero
        return chain

    def cauchy_bound(self) -> Fraction:
        """Integer bound C such that every root lies strictly in (-C, C)."""
        if self.degree == 0:
            return Fraction(0)
        largest = max(
            (abs(c / self.lc) for c in self.coeffs[:-1] if c != 0),
            default=Fraction(0),
        )
        return Fraction(floor(largest) + 1)

    def isolate_real_roots(
        self, refine_to: Fraction = Fraction(1, 10**6)
    ) -> List["ExclusionPoint"]:
        """Exact real roots of self.

        Rational roots are returned as :class:`RationalPoint`; irrational
        roots as :class:`IsolatedRoot` with disjoint rational isolating
        intervals of width at most ``refine_to``.  All rational roots are
        divided out first ("deflation"), leaving a square-free-free factor
        with no rational roots for interval isolation.
        """
        if self.degree == 0:
            return []
        monic = self.to_integer().monic()
        # Distinct (square-free) part: repeated roots appear only once.
        repeated = Polynomial.gcd(monic, monic.derivative())
        poly = monic // repeated
        rationals = poly.rational_roots()
        deflated = poly
        for root in rationals:
            deflated = deflated // Polynomial([-root, 1])
        points: List[ExclusionPoint] = [RationalPoint(r) for r in rationals]
        if deflated.degree > 0:
            points.extend(_isolate_irrational(deflated, refine_to))
        points.sort(key=_point_sort_key)
        return points

    # -- display -----------------------------------------------------------

    def __repr__(self) -> str:
        return f"Polynomial({list(self.coeffs)!r})"

    def __str__(self, variable: str = "x") -> str:
        if self.is_zero():
            return "0"
        terms: List[str] = []
        for power in range(self.degree, -1, -1):
            coeff = self.coeffs[power]
            if coeff == 0:
                continue
            sign = "-" if coeff < 0 else ("+" if terms else "")
            # First term keeps no redundant sign; later positives take +.
            magnitude = abs(coeff)
            if power == 0:
                body = _fraction_str(magnitude)
            elif magnitude == 1:
                body = variable if power == 1 else f"{variable}^{power}"
            else:
                factor = _fraction_str(magnitude)
                body = (
                    f"{factor}*{variable}"
                    if power == 1
                    else f"{factor}*{variable}^{power}"
                )
            terms.append(sign + body if sign else body)
        return "".join(terms)


# ---------------------------------------------------------------------------
# Exclusion points: exact rationals or rationally-isolated irrationals
# ---------------------------------------------------------------------------


class ExclusionPoint:
    """A real number excluded from a rational function's domain."""

    def approximate(self) -> float:
        raise NotImplementedError

    def is_rational(self) -> bool:
        raise NotImplementedError


class RationalPoint(ExclusionPoint):
    __slots__ = ("value",)

    def __init__(self, value: Number) -> None:
        self.value = Fraction(value)

    def is_rational(self) -> bool:
        return True

    def approximate(self) -> float:
        return float(self.value)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, RationalPoint) and other.value == self.value

    def __hash__(self) -> int:
        return hash((RationalPoint, self.value))

    def __lt__(self, other: "ExclusionPoint") -> bool:
        return _point_sort_key(self) < _point_sort_key(other)

    def __repr__(self) -> str:
        return f"RationalPoint({self.value})"

    def __str__(self) -> str:
        return _fraction_str(self.value)


class IsolatedRoot(ExclusionPoint):
    """Irrational real root of ``descriptor`` inside ``(lo, hi)``.

    ``descriptor`` is monic, square-free in the sense of carrying no
    repeated roots (it is a factor left after rational-root deflation of a
    square-free factor product), and has no rational roots.  The interval
    contains exactly one real root and no endpoint is a root.
    """

    __slots__ = ("descriptor", "lo", "hi")

    def __init__(
        self, descriptor: Polynomial, lo: Fraction, hi: Fraction
    ) -> None:
        self.descriptor = descriptor.to_integer().monic()
        self.lo, self.hi = Fraction(lo), Fraction(hi)

    def is_rational(self) -> bool:
        return False

    def midpoint(self) -> Fraction:
        return (self.lo + self.hi) / 2

    def approximate(self) -> float:
        return float(self.midpoint())

    def contains(self, x: Fraction) -> bool:
        return self.lo < x < self.hi

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, IsolatedRoot):
            return False
        overlap = self.lo < other.hi and other.lo < self.hi
        return (
            overlap
            and not Polynomial.gcd(self.descriptor, other.descriptor)
            .is_constant()
        )

    def __hash__(self) -> int:
        return hash((IsolatedRoot, self.descriptor.to_integer()))

    def __lt__(self, other: "ExclusionPoint") -> bool:
        return _point_sort_key(self) < _point_sort_key(other)

    def __repr__(self) -> str:
        return f"IsolatedRoot({self.descriptor}, {self.lo}, {self.hi})"

    def __str__(self) -> str:
        decimals = max(6, -int(__import__("math").floor(
            __import__("math").log10(float(self.hi - self.lo))
        )) + 1)
        approx = format(self.approximate(), f".{decimals}f")
        return (
            f"alpha~{approx} in ({_fmt_decimal(self.lo)}, "
            f"{_fmt_decimal(self.hi)}), root of {self.descriptor}"
        )


def _fmt_decimal(value: Fraction, digits: int = 9) -> str:
    text = f"{float(value):.{digits}f}".rstrip("0").rstrip(".")
    return text


def _point_sort_key(point: ExclusionPoint) -> float:
    if isinstance(point, RationalPoint):
        return float(point.value)
    return point.approximate()


def _sign(value: Fraction) -> int:
    return (value > 0) - (value < 0)


def _variations(chain: List[Polynomial], x: Fraction) -> int:
    """Sign changes of the Sturm chain evaluated at x (zeros ignored)."""
    signs = [_sign(poly(x)) for poly in chain if not poly.is_zero()]
    signs = [s for s in signs if s != 0]
    return sum(
        1 for a, b in zip(signs, signs[1:]) if a != b
    )


def _isolate_irrational(
    poly: Polynomial, refine_to: Fraction
) -> List[IsolatedRoot]:
    """Isolate every real root of ``poly``; poly has NO rational roots."""
    chain = poly.sturm_chain()
    bound = poly.cauchy_bound()
    total = _variations(chain, -bound) - _variations(chain, bound)
    if total == 0:
        return []
    intervals: List[IsolatedRoot] = []

    def split(lo: Fraction, hi: Fraction, count: int) -> None:
        if count == 0:
            return
        if count == 1 and hi - lo <= refine_to:
            intervals.append(IsolatedRoot(poly, lo, hi))
            return
        mid = (lo + hi) / 2
        # poly has no rational roots, so mid is never a root.
        left_count = _variations(chain, lo) - _variations(chain, mid)
        right_count = count - left_count
        split(lo, mid, left_count)
        split(mid, hi, right_count)

    split(-bound, bound, total)
    intervals.sort(key=_point_sort_key)
    return intervals


# ---------------------------------------------------------------------------
# Rational function with preserved domain
# ---------------------------------------------------------------------------


class RationalFunction:
    """A reduced polynomial fraction p/q carrying its true domain.

    ``numerator`` and ``denominator`` are the gcd-reduced pair; the
    denominator is normalised to a positive (monic when non-constant)
    leading sign.  ``exclusion_poly`` is a polynomial whose distinct real
    roots are exactly the real numbers where the ORIGINAL expression is
    undefined.  Reduction never removes roots from it, so cancelling a
    factor such as x/x yields 1 while x = 0 stays excluded.
    """

    __slots__ = (
        "numerator",
        "denominator",
        "exclusion_poly",
        "steps",
        "_exclusions",
    )

    def __init__(
        self,
        numerator: Union[Polynomial, Number],
        denominator: Union[Polynomial, Number] = 1,
        *,
        exclusion_poly: Union[Polynomial, None] = None,
        steps: Union[List[str], None] = None,
    ) -> None:
        num = numerator if isinstance(numerator, Polynomial) else Polynomial(
            [numerator]
        )
        den = denominator if isinstance(denominator, Polynomial) else (
            Polynomial([denominator])
        )
        if den.is_zero():
            raise ZeroDenominatorError(
                f"cannot form rational function {num}/0: zero denominator"
            )
        if exclusion_poly is None:
            exclusion_poly = den.to_integer()
        self.numerator, self.denominator = self._reduce(num, den)
        self.exclusion_poly = exclusion_poly.to_integer()
        self.steps: List[str] = list(steps) if steps else [
            self._formation_step(num, den, self.exclusion_poly)
        ]
        self._exclusions: Union[List[ExclusionPoint], None] = None

    @staticmethod
    def _reduce(
        num: Polynomial, den: Polynomial
    ) -> Tuple[Polynomial, Polynomial]:
        common = Polynomial.gcd(num, den)
        if not common.is_constant():
            num = num // common
            den = den // common
        # Denominator leading sign normalisation (positive leading term).
        if den.lc < 0:
            num = num.scale(-1)
            den = den.scale(-1)
        if den.degree == 0:
            # Constant denominator: normalise to exactly 1.
            num = num.scale(Fraction(1) / den.coeffs[0])
            den = Polynomial([1])
        else:
            # Monic denominator fixes the overall sign convention.
            num = num.scale(Fraction(1) / den.lc)
            den = den.monic()
        return num, den

    @staticmethod
    def _formation_step(
        num: Polynomial, den: Polynomial, exclusion_poly: Polynomial
    ) -> str:
        return (
            f"Form ({num})/({den}); applicable on R minus real zeros of "
            f"the original denominator ({exclusion_poly})."
        )

    # -- construction helpers ---------------------------------------------

    @classmethod
    def _make(
        cls,
        num: Polynomial,
        den: Polynomial,
        exclusion_poly: Polynomial,
        steps: List[str],
    ) -> "RationalFunction":
        obj = cls.__new__(cls)
        reduced_num, reduced_den = cls._reduce(num, den)
        obj.numerator = reduced_num
        obj.denominator = reduced_den
        obj.exclusion_poly = exclusion_poly.to_integer()
        obj.steps = steps
        obj._exclusions = None
        return obj

    @classmethod
    def variable(cls) -> "RationalFunction":
        return cls(Polynomial([0, 1]), Polynomial([1]))

    # -- domain ------------------------------------------------------------

    @property
    def exclusions(self) -> List[ExclusionPoint]:
        """Distinct real roots of the exclusion polynomial (cached)."""
        if self._exclusions is None:
            self._exclusions = self.exclusion_poly.isolate_real_roots()
        return list(self._exclusions)

    def domain_text(self) -> str:
        points = self.exclusions
        if not points:
            return "R (no excluded points)"
        return "R \\ {" + ", ".join(str(p) for p in points) + "}"

    def is_defined(self, x: Fraction) -> bool:
        return self.exclusion_poly(x) != 0

    def __call__(self, x: Fraction) -> Fraction:
        x = Fraction(x)
        if self.exclusion_poly(x) == 0:
            raise ZeroDivisionError(
                f"expression undefined at x = {x} (excluded point)"
            )
        return self.numerator(x) / self.denominator(x)

    @property
    def is_zero_function(self) -> bool:
        return self.numerator.is_zero()

    # -- arithmetic; each result inherits the union of exclusion sets ------

    def __add__(
        self, other: Union["RationalFunction", Number]
    ) -> "RationalFunction":
        if not isinstance(other, RationalFunction):
            other = RationalFunction(Polynomial([other]), Polynomial([1]))
        num = self.numerator * other.denominator + (
            other.numerator * self.denominator
        )
        den = self.denominator * other.denominator
        exclusion_poly = self._union_exclusion(other)
        steps = self._combine_steps(other, "+", exclusion_poly)
        return RationalFunction._make(num, den, exclusion_poly, steps)

    __radd__ = __add__

    def __sub__(
        self, other: Union["RationalFunction", Number]
    ) -> "RationalFunction":
        if not isinstance(other, RationalFunction):
            other = RationalFunction(Polynomial([other]), Polynomial([1]))
        num = self.numerator * other.denominator - (
            other.numerator * self.denominator
        )
        den = self.denominator * other.denominator
        exclusion_poly = self._union_exclusion(other)
        steps = self._combine_steps(other, "-", exclusion_poly)
        return RationalFunction._make(num, den, exclusion_poly, steps)

    def __rsub__(self, other: Number) -> "RationalFunction":
        return RationalFunction(Polynomial([other])) - self

    def __mul__(
        self, other: Union["RationalFunction", Number]
    ) -> "RationalFunction":
        if not isinstance(other, RationalFunction):
            other = RationalFunction(Polynomial([other]), Polynomial([1]))
        num = self.numerator * other.numerator
        den = self.denominator * other.denominator
        exclusion_poly = self._union_exclusion(other)
        steps = self._combine_steps(other, "*", exclusion_poly)
        return RationalFunction._make(num, den, exclusion_poly, steps)

    __rmul__ = __mul__

    def __truediv__(
        self, other: Union["RationalFunction", Number]
    ) -> "RationalFunction":
        if not isinstance(other, RationalFunction):
            other = RationalFunction(Polynomial([other]), Polynomial([1]))
        if other.numerator.is_zero():
            raise ZeroDenominatorError(
                "division by the zero rational function is undefined; "
                "all of the divisor's domain is excluded"
            )
        num = self.numerator * other.denominator
        den = self.denominator * other.numerator
        # Divisor's numerator zeros add new excluded points.
        exclusion_poly = self.exclusion_poly * other.exclusion_poly * (
            other.numerator.to_integer()
        )
        steps = self.steps + other.steps + [
            f"Apply division (f)/(g): valid only where both sides are "
            f"defined AND g != 0; union exclusion polynomial is now "
            f"({exclusion_poly.to_integer()})."
        ]
        return RationalFunction._make(num, den, exclusion_poly, steps)

    def _union_exclusion(
        self, other: "RationalFunction"
    ) -> Polynomial:
        return self.exclusion_poly * other.exclusion_poly

    def _combine_steps(
        self,
        other: "RationalFunction",
        operator: str,
        exclusion_poly: Polynomial,
    ) -> List[str]:
        return self.steps + other.steps + [
            f"Apply '{operator}': valid on the intersection of both "
            f"operands' domains; exclusion polynomial updated to the union "
            f"({exclusion_poly.to_integer()}), then reduce by gcd without "
            f"deleting any excluded root."
        ]

    # -- display -----------------------------------------------------------

    def __str__(self) -> str:
        num, den = self.numerator, self.denominator
        if den == Polynomial([1]):
            return str(num)
        return f"({num})/({den})"

    def explain(self) -> str:
        lines = ["Transformation log (each line states its domain):"]
        lines.extend(f"  {i + 1}. {step}" for i, step in enumerate(self.steps))
        lines.append(f"Reduced form (on its domain): {self}")
        lines.append(f"Domain: {self.domain_text()}")
        return "\n".join(lines)
