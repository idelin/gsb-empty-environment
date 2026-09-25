"""Runnable demo: sparse truncation of a 3-layer chain under a global
L-inf error budget, with certificate verification by vertex enumeration.

Run from the repository root:  python3.11 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from sparse_truncator import ChainTruncator, check_certificate


def build_chain() -> ChainTruncator:
    # Layer 0: 3x3, mixed signs, some tiny entries.
    m0 = (3, 3, [
        (0, 0, 0.5), (0, 1, -0.2), (0, 2, 0.01),
        (1, 0, 0.3), (1, 1, 0.4), (1, 2, -0.02),
        (2, 0, -0.1), (2, 1, 0.05), (2, 2, 0.6),
    ])
    # Layer 1: 3x3 with negative coefficients.
    m1 = (3, 3, [
        (0, 0, 1.2), (0, 1, 0.01), (0, 2, -0.3),
        (1, 0, 0.02), (1, 1, -0.8), (1, 2, 0.4),
        (2, 0, 0.5), (2, 1, 0.03), (2, 2, 0.9),
    ])
    # Layer 2: 2x3 with large row norms -> amplifies earlier errors.
    m2 = (2, 3, [
        (0, 0, 4.0), (0, 1, -3.5), (0, 2, 2.0),
        (1, 0, 0.1), (1, 1, 0.05), (1, 2, -6.0),
    ])
    box = [(-1.0, 1.0), (0.5, 1.5), (-2.0, -1.0)]  # asymmetric, nonzero-width
    return ChainTruncator([m0, m1, m2], box)


def main() -> None:
    chain = build_chain()
    budget = 0.5

    print("=== 1. Greedy allocation under global L-inf budget", budget, "===")
    result = chain.allocate(error_budget=budget)
    print(f"satisfied: {result.satisfied}  ({result.message})")
    print(f"total nonzeros kept: {result.total_nnz}")
    print(result.certificate.render())

    print("\n=== 2. Verify certificate by enumerating input-box vertices ===")
    report = check_certificate(chain)
    print(f"vertices enumerated : {report.num_vertices}")
    print(f"true max deviation  : {report.observed_linf:.6e} "
          f"at vertex {report.argmax_vertex}")
    print(f"certified bound     : {report.certified_linf:.6e}")
    print(f"certificate covers true deviation: {report.ok}")
    assert report.ok

    print("\n=== 3. Amplification: small early errors, large later norms ===")
    for lc in result.certificate.layers:
        print(f"layer {lc.layer}: local injected error is amplified to "
              f"L-inf {lc.linf:.6e} at the chain output")
    print(f"global bound {result.certificate.linf:.6e} is the propagated "
          "sum, not a per-layer reuse of the same budget")

    print("\n=== 4. Revocable approximation choices (undo) ===")
    before = chain.certificate().linf
    chain.drop(0, 0, 0)                      # drop a large entry -> big error
    after_drop = chain.certificate().linf
    chain.undo()                             # revoke it
    after_undo = chain.certificate().linf
    print(f"bound before drop: {before:.6e}; after drop: {after_drop:.6e}; "
          f"after undo: {after_undo:.6e}")
    assert after_undo == before

    print("\n=== 5. Local input modification -> incremental certificate update ===")
    chain.set_input_interval(0, -0.5, 0.75)  # shrink/move one input coordinate
    updated = chain.certificate()
    print(f"bound after input update: {updated.linf:.6e}")
    assert check_certificate(chain).ok       # certificate still covers truth
    chain.undo()                             # restore original input interval
    print(f"bound after undoing input update: {chain.certificate().linf:.6e}")

    print("\n=== 6. Unsatisfiable budget: keep exact part and report ===")
    chain2 = build_chain()
    res2 = chain2.allocate(error_budget=0.05, max_total_nnz=3)
    print(f"satisfied: {res2.satisfied}  ({res2.message})")
    total_orig = sum(len(chain2.original_entries(i))
                     for i in range(len(chain2.shapes)))
    print(f"nonzeros kept: {res2.total_nnz} "
          f"(all {total_orig} original entries restored)")
    assert not res2.satisfied
    assert res2.certificate.linf == 0.0      # exact part kept -> zero error

    print("\nAll demo checks passed.")


if __name__ == "__main__":
    main()
