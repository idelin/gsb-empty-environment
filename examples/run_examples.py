"""End-to-end demo: decomposition, rejection, sampling, conditioning, exact check.

Run from the repository root:  python3 examples/run_examples.py
"""
import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from psd_gaussian import (
    InfeasibleConditionError,
    NotPositiveSemidefiniteError,
    audit_samples,
    condition_gaussian,
    decompose,
    exact_check_psd,
    exact_verify_decomposition,
    sample_correlated,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("1. PSD decomposition (exact/deterministic residual)")
    cov = [[4.0, 1.0, 1.0], [1.0, 3.0, 0.5], [1.0, 0.5, 2.0]]
    dec = decompose(cov)
    print(f"eigenvalues : {[f'{v:.6f}' for v in dec.eigenvalues]}")
    print(f"rank        : {dec.rank}")
    print(f"residual    : ||Sigma - A A^T||_F = {dec.residual_fro:.3e} "
          f"(max abs {dec.residual_max:.3e})")
    print(f"truncated   : {dec.truncated_eigenvalues} (no jitter added)")

    show("2. Non-PSD input is rejected with a witness direction")
    bad = [[1.0, 2.0], [2.0, 1.0]]
    try:
        decompose(bad)
    except NotPositiveSemidefiniteError as err:
        print(f"rejected    : {err}")
        print(f"witness v   : {[f'{x:+.6f}' for x in err.witness]}")

    show("3. Correlated sampling (empirical audit, seed recorded)")
    mean = [1.0, -2.0, 0.5]
    samples, record = sample_correlated(dec, mean, 5000, random.Random(2024), seed=2024)
    audit = audit_samples(samples, mean, cov, record)
    print(f"record      : seed={record.seed}, n={record.n_samples} [{record.kind}]")
    print(f"sample mean : {[f'{x:+.4f}' for x in audit.sample_mean]} "
          f"(max err {audit.mean_error_max:.4f})")
    print(f"cov error   : ||S - Sigma||_F = {audit.cov_error_fro:.4f} "
          f"(relative {audit.cov_error_relative:.2%}) [{audit.kind}]")

    show("4. Conditional Gaussian given x1 + x2 + x3 = 2")
    cond = condition_gaussian(mean, cov, [[1.0, 1.0, 1.0]], [2.0])
    print(f"cond mean   : {[f'{x:+.6f}' for x in cond.mean]}")
    print(f"zero-var dir: {[[round(x, 6) for x in d] for d in cond.zero_variance_directions]}")
    csamples, _ = sample_correlated(cond.decomposition, cond.mean, 3, random.Random(1), seed=1)
    for s in csamples:
        print(f"  sample {[f'{x:+.4f}' for x in s]}  sum={sum(s):.10f}")
    try:
        condition_gaussian([0.0, 3.0], [[1.0, 0.0], [0.0, 0.0]], [[0.0, 1.0]], [4.0])
    except InfeasibleConditionError as err:
        print(f"infeasible  : {err}")

    show("5. Exact rational verification (Fraction arithmetic)")
    small = [[2, 1, 0], [1, 2, 1], [0, 1, 2]]
    report = exact_check_psd(small)
    print(f"is_psd      : {report.is_psd} [{report.kind}]")
    print(f"D           : {report.D}")
    print(f"L D L^T == Sigma : {exact_verify_decomposition(report.L, report.D, small)}")
    bad_report = exact_check_psd([[1, 2], [2, 1]])
    print(f"indefinite  : is_psd={bad_report.is_psd}, "
          f"witness={bad_report.witness}, v^T Sigma v={bad_report.witness_value}")


if __name__ == "__main__":
    main()
