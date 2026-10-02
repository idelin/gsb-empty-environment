"""Updatable diagonal (Jacobi) preconditioner."""


class ZeroDiagonalError(ValueError):
    """Raised when the matrix diagonal contains zeros and policy is 'raise'."""

    def __init__(self, indices):
        self.indices = list(indices)
        super().__init__(f"zero diagonal entries at indices {self.indices}")


class DiagonalPreconditioner:
    """M = diag(A). ``update_from_matrix`` refreshes it after matrix changes.

    ``zero_policy`` controls zero diagonal entries:
      - ``"unit"``: replace zeros by 1.0 and count them in ``zero_replacements``
      - ``"raise"``: raise :class:`ZeroDiagonalError` carrying the indices
    """

    def __init__(self, size):
        if size <= 0:
            raise ValueError("preconditioner size must be positive")
        self.size = size
        self.diag = [1.0] * size
        self.zero_replacements = 0

    def update_from_matrix(self, matrix, zero_policy="unit"):
        d = matrix.diagonal()
        if len(d) != self.size:
            raise ValueError(
                f"matrix diagonal has length {len(d)}, preconditioner size is {self.size}"
            )
        zeros = [i for i, v in enumerate(d) if v == 0.0]
        if zeros:
            if zero_policy == "raise":
                raise ZeroDiagonalError(zeros)
            if zero_policy != "unit":
                raise ValueError(f"unknown zero_policy {zero_policy!r}")
        self.zero_replacements = len(zeros)
        self.diag = [1.0 if v == 0.0 else v for v in d]
        return self

    def apply(self, r):
        if len(r) != self.size:
            raise ValueError(
                f"vector length {len(r)} does not match preconditioner size {self.size}"
            )
        diag = self.diag
        return [ri / di for ri, di in zip(r, diag)]
