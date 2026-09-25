"""Pairwise-complete covariance for sparse observations (pure stdlib).

Design notes
------------
* Missing values (``None`` or ``NaN``) are never treated as zero: each pair
  (i, j) is counted only over rows where BOTH values are present.
* Each pair keeps an independent, mergeable central-moment statistic using
  the Welford/Chan parallel update, so huge means with tiny variance are not
  destroyed by naive "sum of squares minus n*mean**2" cancellation.
* Denominator rule is explicit: covariance = C / (n - ddof).  When
  n - ddof <= 0 the value is undefined and reported as NaN; it is never
  silently invented.
* Shards merge associatively.  Batches are tracked by explicit id: re-adding
  an identical batch is an idempotent no-op, conflicting payloads raise, and
  retracting an unknown id is rejected.  Counts can never go negative.
* Pairwise-complete covariance is not guaranteed to be positive semidefinite.
  ``psd_check`` diagnoses this with a self-written dense Jacobi eigensolver
  and returns a witness vector v with v^T S v < 0.  The matrix is never
  silently altered.

Python 3.11, standard library only.  No NumPy / SciPy.
"""

from __future__ import annotations

import hashlib
import math
from dataclasses import dataclass, field


def is_missing(value) -> bool:
    """None and NaN count as missing; every other value must be numeric."""
    return value is None or (isinstance(value, float) and math.isnan(value))


class PairMoment:
    """Mergeable two-variable central moment for one pair.

    Maintains n, the pairwise-complete means (mean_x, mean_y) and the
    co-moment C = sum_k (x_k - mean_x) * (y_k - mean_y) in a numerically
    stable form (Welford online update; Chan parallel merge/unmerge).
    """

    __slots__ = ("n", "mean_x", "mean_y", "c")

    def __init__(self) -> None:
        self.n: int = 0
        self.mean_x: float = 0.0
        self.mean_y: float = 0.0
        self.c: float = 0.0

    def add(self, x: float, y: float) -> None:
        n = self.n + 1
        dx = x - self.mean_x
        dy = y - self.mean_y
        self.mean_x += dx / n
        self.mean_y += dy / n
        # Welford pairwise update (new y-mean in the second factor).
        self.c += dx * (y - self.mean_y)
        self.n = n

    def merged(self, other: "PairMoment") -> "PairMoment":
        """Return the statistic for self U other (Chan parallel formula)."""
        out = PairMoment()
        if other.n == 0:
            out.copy_from(self)
            return out
        if self.n == 0:
            out.copy_from(other)
            return out
        n = self.n + other.n
        dx = other.mean_x - self.mean_x
        dy = other.mean_y - self.mean_y
        out.n = n
        out.mean_x = self.mean_x + dx * other.n / n
        out.mean_y = self.mean_y + dy * other.n / n
        out.c = self.c + other.c + dx * dy * self.n * other.n / n
        return out

    def without(self, other: "PairMoment") -> "PairMoment":
        """Inverse of :meth:`merged`: recover self minus other.

        Raises ValueError if the subtraction would make the count negative
        (i.e. retracting evidence that was never recorded).
        """
        n_a = self.n - other.n
        if n_a < 0:
            raise ValueError(
                f"cannot retract {other.n} observations from count {self.n}"
            )
        out = PairMoment()
        if n_a == 0:
            return out
        mean_ax = (self.n * self.mean_x - other.n * other.mean_x) / n_a
        mean_ay = (self.n * self.mean_y - other.n * other.mean_y) / n_a
        dx = other.mean_x - mean_ax
        dy = other.mean_y - mean_ay
        out.n = n_a
        out.mean_x = mean_ax
        out.mean_y = mean_ay
        out.c = self.c - other.c - dx * dy * n_a * other.n / self.n
        return out

    def covariance(self, ddof: int = 1) -> float:
        """C / (n - ddof); NaN when n - ddof <= 0 (undefined denominator)."""
        if ddof < 0:
            raise ValueError("ddof must be non-negative")
        denom = self.n - ddof
        if denom <= 0:
            return math.nan
        return self.c / denom

    def copy_from(self, other: "PairMoment") -> None:
        self.n = other.n
        self.mean_x = other.mean_x
        self.mean_y = other.mean_y
        self.c = other.c

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return (
            f"PairMoment(n={self.n}, mean_x={self.mean_x!r}, "
            f"mean_y={self.mean_y!r}, c={self.c!r})"
        )


def _check_index(index: int, dim: int) -> None:
    if not isinstance(index, int) or isinstance(index, bool):
        raise TypeError("index must be an int")
    if not 0 <= index < dim:
        raise IndexError(f"index {index} out of range for dimension {dim}")


class ShardAccumulator:
    """Accumulates per-pair mergeable moments over rows of fixed dimension.

    ``dim`` is given explicitly; ``None``/NaN entries are missing and are
    never coerced to zero.
    """

    def __init__(self, dim: int) -> None:
        if not isinstance(dim, int) or isinstance(dim, bool) or dim <= 0:
            raise ValueError("dim must be a positive int")
        self.dim = dim
        # Only the upper triangle (i <= j) holds live statistics.
        self._pairs: list[list[PairMoment]] = [
            [PairMoment() for _ in range(dim)] for _ in range(dim)
        ]

    def pair(self, i: int, j: int) -> PairMoment:
        _check_index(i, self.dim)
        _check_index(j, self.dim)
        if i > j:
            i, j = j, i
        return self._pairs[i][j]

    def add_row(self, row) -> None:
        if len(row) != self.dim:
            raise ValueError(
                f"row length {len(row)} does not match dimension {self.dim}"
            )
        values: list = []
        for value in row:
            if is_missing(value):
                values.append(None)
            elif isinstance(value, bool) or not isinstance(value, (int, float)):
                raise TypeError(f"non-numeric value {value!r} in row")
            else:
                values.append(float(value))
        for i in range(self.dim):
            x = values[i]
            if x is None:
                continue
            for j in range(i, self.dim):
                y = values[j]
                if y is None:
                    continue
                self._pairs[i][j].add(x, y)

    def add_rows(self, rows) -> int:
        count = 0
        for row in rows:
            self.add_row(row)
            count += 1
        return count

    def merged(self, other: "ShardAccumulator") -> "ShardAccumulator":
        if other.dim != self.dim:
            raise ValueError("cannot merge shards of different dimensions")
        out = ShardAccumulator(self.dim)
        for i in range(self.dim):
            for j in range(i, self.dim):
                out._pairs[i][j].copy_from(
                    self._pairs[i][j].merged(other._pairs[i][j])
                )
        return out

    def without(self, other: "ShardAccumulator") -> "ShardAccumulator":
        if other.dim != self.dim:
            raise ValueError("cannot retract shard of different dimension")
        out = ShardAccumulator(self.dim)
        for i in range(self.dim):
            for j in range(i, self.dim):
                out._pairs[i][j].copy_from(
                    self._pairs[i][j].without(other._pairs[i][j])
                )
        return out

    def count(self, i: int, j: int) -> int:
        return self.pair(i, j).n

    def pair_counts(self) -> list:
        return [
            [self.pair(i, j).n for j in range(self.dim)]
            for i in range(self.dim)
        ]

    def covariance(self, i: int, j: int, ddof: int = 1) -> float:
        return self.pair(i, j).covariance(ddof)

    def covariance_matrix(self, ddof: int = 1) -> list:
        return [
            [self.pair(i, j).covariance(ddof) for j in range(self.dim)]
            for i in range(self.dim)
        ]

    def undefined_pairs(self, ddof: int = 1) -> list:
        bad = []
        for i in range(self.dim):
            for j in range(i, self.dim):
                if self.pair(i, j).n - ddof <= 0:
                    bad.append((i, j))
        return bad

    def total_pair_observations(self) -> int:
        return sum(
            self._pairs[i][j].n
            for i in range(self.dim)
            for j in range(i, self.dim)
        )


def hash_rows(dim: int, rows) -> str:
    """Stable digest of a batch payload, used for idempotent re-adds."""
    digest = hashlib.sha256()
    digest.update(f"dim={dim};".encode())
    for row in rows:
        if len(row) != dim:
            raise ValueError("row length does not match dimension")
        for value in row:
            if is_missing(value):
                digest.update(b"NA;")
            elif isinstance(value, bool) or not isinstance(value, (int, float)):
                raise TypeError(f"non-numeric value {value!r} in row")
            else:
                digest.update((repr(float(value)) + ";").encode())
        digest.update(b"\n")
    return digest.hexdigest()


@dataclass
class RetractionRecord:
    """Audit evidence for one retracted batch."""

    batch_id: str
    row_count: int
    digest: str
    pair_observations: int


@dataclass
class VerificationReport:
    """Result of cross-checking the merge against an independent recompute."""

    ok: bool
    max_count_mismatch: int
    max_moment_deviation: float
    checked_pairs: int
    message: str = ""


class UnknownBatchError(KeyError):
    """Raised when retracting a batch id that was never registered."""


class ConflictingBatchError(ValueError):
    """Raised when an existing batch id is re-added with a different payload."""


class CovarianceLedger:
    """Holds named batches and answers pairwise covariance over them.

    Batches are independent shards.  The merged result is recomputed from
    batch evidence on demand (insertion order), which makes retraction exact
    and order-independent; :meth:`verify` cross-checks it against an
    independent merge order.
    """

    def __init__(self, dim: int) -> None:
        self.dim = dim
        self._batches: dict = {}
        self._digests: dict = {}
        self._row_counts: dict = {}
        self.retraction_log: list = []

    def add_batch(self, batch_id, rows) -> bool:
        """Register a batch under ``batch_id``.

        Idempotent: re-adding the exact same payload is a no-op returning
        False.  Re-adding the same id with a different payload raises.
        Returns True when a new batch was recorded.
        """
        if not isinstance(batch_id, str) or not batch_id:
            raise ValueError("batch_id must be a non-empty string")
        payload = list(rows)
        digest = hash_rows(self.dim, payload)
        if batch_id in self._batches:
            if self._digests[batch_id] == digest:
                return False
            raise ConflictingBatchError(
                f"batch {batch_id!r} already exists with different payload"
            )
        acc = ShardAccumulator(self.dim)
        row_count = acc.add_rows(payload)
        self._batches[batch_id] = acc
        self._digests[batch_id] = digest
        self._row_counts[batch_id] = row_count
        return True

    def retract_batch(self, batch_id: str) -> RetractionRecord:
        """Remove a batch by id; unknown ids are rejected, never a silent no-op."""
        if batch_id not in self._batches:
            raise UnknownBatchError(f"batch {batch_id!r} is not registered")
        acc = self._batches.pop(batch_id)
        digest = self._digests.pop(batch_id)
        row_count = self._row_counts.pop(batch_id)
        record = RetractionRecord(
            batch_id=batch_id,
            row_count=row_count,
            digest=digest,
            pair_observations=acc.total_pair_observations(),
        )
        self.retraction_log.append(record)
        return record

    def batch_ids(self) -> list:
        return list(self._batches)

    def merged(self) -> ShardAccumulator:
        out = ShardAccumulator(self.dim)
        for acc in self._batches.values():
            out = out.merged(acc)
        return out

    def _merged_reverse(self) -> ShardAccumulator:
        out = ShardAccumulator(self.dim)
        for acc in reversed(list(self._batches.values())):
            out = out.merged(acc)
        return out

    def count(self, i: int, j: int) -> int:
        return self.merged().count(i, j)

    def covariance(self, i: int, j: int, ddof: int = 1) -> float:
        return self.merged().covariance(i, j, ddof)

    def covariance_matrix(self, ddof: int = 1) -> list:
        return self.merged().covariance_matrix(ddof)

    def pair_counts(self) -> list:
        return self.merged().pair_counts()

    def undefined_pairs(self, ddof: int = 1) -> list:
        return self.merged().undefined_pairs(ddof)

    def verify(self, tol: float = 1e-9) -> VerificationReport:
        """Cross-check forward-merge vs reverse-merge and non-negativity.

        Counts must agree exactly; means/co-moments within ``tol``; every
        pair count must be non-negative.
        """
        forward = self.merged()
        reverse = self._merged_reverse()
        max_count_mismatch = 0
        max_deviation = 0.0
        checked = 0
        negative = []
        for i in range(self.dim):
            for j in range(i, self.dim):
                a = forward.pair(i, j)
                b = reverse.pair(i, j)
                checked += 1
                if a.n < 0 or b.n < 0:
                    negative.append((i, j))
                max_count_mismatch = max(max_count_mismatch, abs(a.n - b.n))
                for av, bv in (
                    (a.mean_x, b.mean_x),
                    (a.mean_y, b.mean_y),
                    (a.c, b.c),
                ):
                    scale = max(1.0, abs(av), abs(bv))
                    max_deviation = max(max_deviation, abs(av - bv) / scale)
        ok = (
            max_count_mismatch == 0
            and max_deviation <= tol
            and not negative
        )
        message = "ok" if ok else (
            f"mismatch: counts={max_count_mismatch} "
            f"deviation={max_deviation:.3e} negative={negative}"
        )
        return VerificationReport(
            ok=ok,
            max_count_mismatch=max_count_mismatch,
            max_moment_deviation=max_deviation,
            checked_pairs=checked,
            message=message,
        )


def dense_pairwise_covariance(rows, dim: int, ddof: int = 1):
    """Self-written dense two-pass reference (small-scale verification).

    Returns (covariance_matrix, counts).  Missing values are skipped
    pairwise (never zero); undefined pairs (n - ddof <= 0) are NaN.
    """
    rows = [list(r) for r in rows]
    for r in rows:
        if len(r) != dim:
            raise ValueError("row length does not match dimension")
    cov = [[math.nan] * dim for _ in range(dim)]
    counts = [[0] * dim for _ in range(dim)]
    for i in range(dim):
        for j in range(dim):
            pairs = [
                (float(r[i]), float(r[j]))
                for r in rows
                if not is_missing(r[i]) and not is_missing(r[j])
            ]
            n = len(pairs)
            counts[i][j] = n
            if n - ddof <= 0:
                continue
            mx = sum(x for x, _ in pairs) / n
            my = sum(y for _, y in pairs) / n
            c = sum((x - mx) * (y - my) for x, y in pairs)
            cov[i][j] = c / (n - ddof)
    return cov, counts


def _jacobi_eigen(matrix, max_sweeps: int = 200):
    """Cyclic Jacobi eigendecomposition of a small symmetric matrix."""
    n = len(matrix)
    a = [[float(matrix[i][j]) for j in range(n)] for i in range(n)]
    vecs = [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]
    scale = max([abs(a[i][i]) for i in range(n)] + [1.0])
    for _ in range(max_sweeps):
        off = max(
            (abs(a[p][q]) for p in range(n) for q in range(p + 1, n)),
            default=0.0,
        )
        if off <= 1e-13 * scale:
            break
        for p in range(n - 1):
            for q in range(p + 1, n):
                apq = a[p][q]
                if apq == 0.0:
                    continue
                theta = (a[q][q] - a[p][p]) / (2.0 * apq)
                sign = 1.0 if theta >= 0.0 else -1.0
                t = sign / (abs(theta) + math.sqrt(theta * theta + 1.0))
                c = 1.0 / math.sqrt(t * t + 1.0)
                s = t * c
                for k in range(n):
                    akp, akq = a[k][p], a[k][q]
                    a[k][p] = c * akp - s * akq
                    a[k][q] = s * akp + c * akq
                for k in range(n):
                    apk, aqk = a[p][k], a[q][k]
                    a[p][k] = c * apk - s * aqk
                    a[q][k] = s * apk + c * aqk
                for k in range(n):
                    vkp, vkq = vecs[k][p], vecs[k][q]
                    vecs[k][p] = c * vkp - s * vkq
                    vecs[k][q] = s * vkp + c * vkq
    return [a[i][i] for i in range(n)], vecs


def quadratic_form(matrix, vec) -> float:
    n = len(vec)
    return sum(
        vec[i] * matrix[i][j] * vec[j] for i in range(n) for j in range(n)
    )


@dataclass
class PSDReport:
    """Diagnosis of positive semidefiniteness; never mutates the matrix."""

    determinate: bool
    is_psd: bool
    min_eigenvalue: float | None
    witness: list | None = None
    witness_quadratic_form: float | None = None
    undefined_pairs: list = field(default_factory=list)
    message: str = ""

    def summary(self) -> str:
        return (
            f"determinate={self.determinate} is_psd={self.is_psd} "
            f"min_eigenvalue={self.min_eigenvalue} "
            f"undefined_pairs={self.undefined_pairs} :: {self.message}"
        )


def psd_check(matrix, tol: float = 1e-9) -> PSDReport:
    """Check positive semidefiniteness without modifying ``matrix``.

    If the matrix is not PSD, returns a witness vector (eigenvector of the
    minimum eigenvalue) whose quadratic form is strictly negative.  If some
    entries are undefined (NaN), the status is reported as indeterminate
    with the offending pairs listed.
    """
    n = len(matrix)
    if any(len(row) != n for row in matrix):
        raise ValueError("matrix must be square")

    undefined = sorted(
        {
            (min(i, j), max(i, j))
            for i in range(n)
            for j in range(n)
            if math.isnan(matrix[i][j])
        }
    )
    if undefined:
        return PSDReport(
            determinate=False,
            is_psd=False,
            min_eigenvalue=None,
            undefined_pairs=undefined,
            message=(
                "PSD status undeterminable: covariance undefined "
                "(n - ddof <= 0) for the listed pairs; matrix not modified."
            ),
        )

    eigenvalues, vecs = _jacobi_eigen(matrix)
    k = min(range(n), key=lambda idx: eigenvalues[idx])
    witness = [vecs[row][k] for row in range(n)]
    q = quadratic_form(matrix, witness)
    scale = max([abs(matrix[i][i]) for i in range(n)] + [1.0])
    if eigenvalues[k] < -tol * scale:
        return PSDReport(
            determinate=True,
            is_psd=False,
            min_eigenvalue=eigenvalues[k],
            witness=witness,
            witness_quadratic_form=q,
            message=(
                "Pairwise-complete covariance matrix is NOT positive "
                "semidefinite; witness vector has negative quadratic form. "
                "Matrix returned unchanged."
            ),
        )
    return PSDReport(
        determinate=True,
        is_psd=True,
        min_eigenvalue=eigenvalues[k],
        message="Matrix is positive semidefinite within tolerance.",
    )
