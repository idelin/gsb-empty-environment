"""Incremental monitoring of per-equivalence-class vs. overall distributions.

Only synthetic data is handled.  Count vectors are maintained incrementally on
record insertion / withdrawal (O(1) per mutation); distances are recomputed
exactly from the updated counts (O(support size)).  Every mutation changes the
overall baseline and bumps ``baseline_version``, which invalidates all prior
class evaluations -- a changed overall baseline forces every class to be
re-evaluated.

Statistical distance monitoring is NOT a formal privacy proof; see README.md.
"""

from dataclasses import dataclass, field
from fractions import Fraction

from distances import emd_1d, emd_general, total_variation

STATUS_OK = "OK"
STATUS_EMPTY_CLASS = "SKIPPED_EMPTY_CLASS"
STATUS_EMPTY_BASELINE = "SKIPPED_EMPTY_BASELINE"


def _as_fraction(value):
    if isinstance(value, Fraction):
        return value
    if isinstance(value, float):
        return Fraction(str(value))
    return Fraction(value)


@dataclass
class Evaluation:
    """Result of comparing one equivalence class against the overall baseline."""

    class_key: object
    status: str
    n_class: int
    n_baseline: int
    baseline_version: int
    tv: object = None
    tv_bucket_contribs: dict = None
    emd: object = None
    emd_contribs: dict = None
    tv_evidence: dict = None
    emd_evidence: dict = None
    flow: object = None
    emd_mode: str = None

    @property
    def evaluated(self):
        return self.status == STATUS_OK


def _threshold_evidence(total, labelled_contribs, threshold):
    """Per-bucket contributions sorted by size with cumulative evidence.

    ``labelled_contribs`` is a list of ``(bucket_label, Fraction)``.  The trail
    stops at the first prefix whose cumulative mass strictly exceeds the
    threshold, giving the smallest ordered set of buckets proving a breach.
    """
    if threshold is None:
        return None
    threshold = _as_fraction(threshold)
    ordered = sorted(labelled_contribs, key=lambda item: item[1], reverse=True)
    trail = []
    cumulative = Fraction(0)
    for label, contribution in ordered:
        cumulative += contribution
        trail.append(
            {
                "bucket": label,
                "contribution": contribution,
                "cumulative": cumulative,
            }
        )
        if cumulative > threshold:
            break
    return {
        "threshold": threshold,
        "total": total,
        "exceeded": total > threshold,
        "contributions": trail,
    }


class DistributionMonitor:
    """Maintains overall counts and per-class counts plus distance evidence.

    Parameters
    ----------
    support:
        Ordered list of sensitive-value bucket labels.
    positions:
        Numeric positions of the buckets for the ordered 1-D transportation
        distance (default: ``0, 1, ..., k-1``).  Ignored when
        ``cost_matrix`` is given.
    cost_matrix:
        Optional integer cost matrix aligned with ``support``.  When present,
        the transportation distance is computed by a genuine min-cost flow
        instead of the 1-D cumulative formula.
    tv_threshold, emd_threshold:
        Optional non-negative thresholds; per-bucket threshold evidence is
        produced whenever they are set.
    """

    def __init__(
        self,
        support,
        positions=None,
        cost_matrix=None,
        tv_threshold=None,
        emd_threshold=None,
    ):
        self.support = list(support)
        k = len(self.support)
        if k == 0:
            raise ValueError("support must be non-empty")
        if len(set(self.support)) != k:
            raise ValueError("support labels must be unique")
        if positions is None:
            positions = list(range(k))
        if len(positions) != k:
            raise ValueError("positions must align with the support")
        if cost_matrix is not None and (
            len(cost_matrix) != k or any(len(row) != k for row in cost_matrix)
        ):
            raise ValueError("cost matrix must be aligned with the support")

        self.positions = positions
        self.cost_matrix = cost_matrix
        self.emd_mode = "general_cost_matrix" if cost_matrix is not None else "ordered_1d"
        self.tv_threshold = tv_threshold
        self.emd_threshold = emd_threshold

        self._index = {value: i for i, value in enumerate(self.support)}
        self.baseline_counts = [0] * k
        self.baseline_version = 0
        self._classes = {}
        self._records = {}
        self._last_eval_version = {}

    # ------------------------------------------------------------------
    # Incremental mutation
    # ------------------------------------------------------------------

    def add_record(self, record_id, class_key, sensitive_value):
        """Insert one synthetic record.  Changes baseline: version += 1."""
        if record_id in self._records:
            raise ValueError(f"record {record_id!r} already present")
        value_index = self._index[sensitive_value]
        if class_key not in self._classes:
            self._classes[class_key] = [0] * len(self.support)
        self._classes[class_key][value_index] += 1
        self.baseline_counts[value_index] += 1
        self._records[record_id] = (class_key, value_index)
        self.baseline_version += 1
        return self.baseline_version

    def withdraw_record(self, record_id):
        """Withdraw one previously inserted record.  Changes baseline."""
        if record_id not in self._records:
            raise KeyError(f"record {record_id!r} not found")
        class_key, value_index = self._records.pop(record_id)
        class_counts = self._classes[class_key]
        if class_counts[value_index] <= 0:
            raise RuntimeError("inconsistent class counts")
        class_counts[value_index] -= 1
        self.baseline_counts[value_index] -= 1
        self.baseline_version += 1
        return self.baseline_version

    # ------------------------------------------------------------------
    # Read-only views
    # ------------------------------------------------------------------

    def class_keys(self):
        return list(self._classes.keys())

    def class_counts(self, class_key):
        return list(self._classes[class_key])

    def class_size(self, class_key):
        return sum(self._classes[class_key])

    def baseline_size(self):
        return sum(self.baseline_counts)

    def stale_classes(self):
        """Classes whose last evaluation predates the current baseline."""
        return [
            key
            for key in self._classes
            if self._last_eval_version.get(key) != self.baseline_version
        ]

    # ------------------------------------------------------------------
    # Evaluation
    # ------------------------------------------------------------------

    def evaluate(self, class_key):
        """Compare one class against the current overall baseline exactly."""
        if class_key not in self._classes:
            raise KeyError(f"unknown class {class_key!r}")
        class_counts = self._classes[class_key]
        n_class = sum(class_counts)
        n_baseline = sum(self.baseline_counts)

        if n_baseline == 0:
            evaluation = Evaluation(
                class_key=class_key,
                status=STATUS_EMPTY_BASELINE,
                n_class=n_class,
                n_baseline=0,
                baseline_version=self.baseline_version,
            )
        elif n_class == 0:
            evaluation = Evaluation(
                class_key=class_key,
                status=STATUS_EMPTY_CLASS,
                n_class=0,
                n_baseline=n_baseline,
                baseline_version=self.baseline_version,
            )
        else:
            tv, tv_contribs = total_variation(class_counts, self.baseline_counts)
            tv_labelled = list(zip(self.support, tv_contribs))
            if self.cost_matrix is not None:
                emd, flow, bucket_contribs = emd_general(
                    class_counts, self.baseline_counts, self.cost_matrix
                )
                emd_contribs = dict(zip(self.support, bucket_contribs))
                emd_labelled = list(emd_contribs.items())
            else:
                result = emd_1d(class_counts, self.baseline_counts, self.positions)
                emd = result["distance"]
                flow = result["flow"]
                labels = [
                    f"{self.support[j]}->{self.support[j + 1]}"
                    for j in range(len(self.support) - 1)
                ]
                emd_contribs = dict(zip(labels, result["gap_contribs"]))
                emd_labelled = list(emd_contribs.items())
            evaluation = Evaluation(
                class_key=class_key,
                status=STATUS_OK,
                n_class=n_class,
                n_baseline=n_baseline,
                baseline_version=self.baseline_version,
                tv=tv,
                tv_bucket_contribs=dict(tv_labelled),
                emd=emd,
                emd_contribs=emd_contribs,
                tv_evidence=_threshold_evidence(tv, tv_labelled, self.tv_threshold),
                emd_evidence=_threshold_evidence(
                    emd, emd_labelled, self.emd_threshold
                ),
                flow=flow,
                emd_mode=self.emd_mode,
            )
        self._last_eval_version[class_key] = self.baseline_version
        return evaluation

    def evaluate_all(self):
        """Re-evaluate every class (required after a baseline version change)."""
        return {key: self.evaluate(key) for key in self._classes}
