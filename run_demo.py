"""Runnable sample: sparse truncation with a conservative chain certificate.

Run with:  python3 run_demo.py
No NumPy/SciPy.  Python 3.11+ (also runs on 3.13).
"""

from sparse_trunc import (
    SparseMatrix,
    TruncatedChain,
    check_corners,
)


def section(title: str) -> None:
    print()
    print("=" * 64)
    print(title)
    print("=" * 64)


def main() -> None:
    section("1. Two-layer chain; layer 1 has a large norm")
    # Dimensions and indices are explicit inputs.
    a0 = SparseMatrix(
        2, 2,
        [(0, 0, 1.0), (0, 1, -0.01), (1, 0, 0.0), (1, 1, 1.0)],
        name="A0")
    a1 = SparseMatrix(
        2, 2,
        [(0, 0, 100.0), (0, 1, 0.0), (1, 0, 0.0), (1, 1, 1.0)],
        name="A1")
    chain = TruncatedChain([a0, a1], input_lo=[-1.0, -1.0],
                           input_hi=[1.0, 1.0])

    print("budget 2.0, per-layer kept-nnz caps [2, 4]")
    cert = chain.schedule_truncate(budget=2.0, nnz_caps=[2, 4])
    print(cert.render())
    corner = check_corners(chain, cert.final_linf_bound)
    print(corner.render())
    print("note: layer-0 local injected error is only 0.01, but the large")
    print("layer-1 norm (100) propagates it to a final bound of 1.0; layers")
    print("are NOT certified independently against the same budget.")

    section("2. Same chain under a tight 0.5 budget: keep exact, report")
    fresh = TruncatedChain(
        [SparseMatrix(2, 2,
                      [(0, 0, 1.0), (0, 1, -0.01),
                       (1, 0, 0.0), (1, 1, 1.0)]),
         SparseMatrix(2, 2,
                      [(0, 0, 100.0), (0, 1, 0.0),
                       (1, 0, 0.0), (1, 1, 1.0)])],
        [-1.0, -1.0], [1.0, 1.0])
    tight = fresh.schedule_truncate(0.5, [2, 4])
    print(tight.render())

    section("3. Negative coefficients and interval propagation")
    neg = SparseMatrix(
        2, 2,
        [(0, 0, -2.0), (0, 1, 3.0), (1, 0, 1.0), (1, 1, -1.0)])
    neg_chain = TruncatedChain([neg], [1.0, 1.0], [2.0, 2.0])
    lo, hi, bound = neg_chain.exact_propagation()
    print("input z0 in [1,2]^2; exact output interval:")
    print("  lo:", lo[-1], " hi:", hi[-1],
          " (negative entries flip endpoints)")

    section("4. Incremental certificate update after a local input change")
    chain.update_input([0.5, 0.5], [0.5, 0.5])
    inc = chain.certify(2.0, [2, 4])
    print(inc.render())

    section("5. Revocable approximation + rollback restores matrix/ledger")
    chain.snapshot("before_revoke")
    chain.restore_entry(0, 0, 1)
    print("after restore_entry(0,0,1): layer0 kept =",
          chain.layers[0].kept_nnz)
    chain.rollback("before_revoke")
    print("after rollback: layer0 dropped =",
          dict(chain.layers[0].dropped))
    undone = chain.undo(2)
    print(f"after undo({undone}) (input update + schedule): layer0 kept =",
          chain.layers[0].kept_nnz, " dropped =", dict(chain.layers[0].dropped))


if __name__ == "__main__":
    main()
