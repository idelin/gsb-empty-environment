"""Dense affine forms with explicit size and indices.

Used as an independent, self-written dense implementation to cross-check the
sparse forms on small cases, and as the arithmetic of the certificate
verifier.  No NumPy/SciPy: coefficients live in a plain list whose length
(the explicit ``size``) is supplied by the caller.
"""

from .affine import AffineForm


class DenseAffineForm:
    """center + coeffs[0]*eps_0 + ... + coeffs[size-1]*eps_{size-1}."""

    __slots__ = ("size", "center", "coeffs")

    def __init__(self, size, center=0.0, coeffs=None):
        self.size = int(size)
        if self.size < 0:
            raise ValueError("size must be non-negative")
        self.center = float(center)
        if coeffs is None:
            self.coeffs = [0.0] * self.size
        else:
            self.coeffs = [float(c) for c in coeffs]
            if len(self.coeffs) != self.size:
                raise ValueError(
                    f"explicit size mismatch: size={self.size} but "
                    f"{len(self.coeffs)} coefficients"
                )

    @classmethod
    def from_sparse(cls, size, form):
        dense = cls(size, form.center)
        for idx, coeff in form.coeffs.items():
            if not 0 <= idx < size:
                raise IndexError(f"symbol index {idx} outside dense size {size}")
            dense.coeffs[idx] = coeff
        return dense

    def to_sparse(self):
        return AffineForm(
            self.center, {i: c for i, c in enumerate(self.coeffs) if c != 0.0}
        )

    def radius(self):
        return sum(abs(c) for c in self.coeffs)

    def interval(self):
        r = self.radius()
        return (self.center - r, self.center + r)

    def add(self, other, sign_b=1.0):
        if self.size != other.size:
            raise ValueError("dense size mismatch")
        return DenseAffineForm(
            self.size,
            self.center + sign_b * other.center,
            [a + sign_b * b for a, b in zip(self.coeffs, other.coeffs)],
        )

    def scaled(self, k):
        return DenseAffineForm(
            self.size, self.center * k, [c * k for c in self.coeffs]
        )

    def shifted(self, c):
        return DenseAffineForm(self.size, self.center + c, self.coeffs)
