"""Runnable demo: exact Sturm isolation with certificates, budget
interruption/resume, endpoint-root rules and revision matching.

Run:  python3.11 demo.py
"""
from fractions import Fraction as F

from sturmiso import (
    Interval,
    isolate,
    normalize,
    resume_isolation,
    revise_polynomial,
    to_string,
    verify_isolation,
    verify_revision,
)
from sturmiso.sturm import count_distinct_roots, sturm_chain


def show_result(res, title):
    print(f"== {title}")
    print(f"   f(x) = {to_string(res.polynomial)}")
    if res.squarefree_factors:
        facs = ", ".join(
            f"f_{fac.index}(x) = {to_string(fac.poly)} "
            f"(multiplicity {fac.multiplicity}, primitive {fac.primitive})"
            for fac in res.squarefree_factors
        )
        print(f"   square-free factors: {facs}")
        print(f"   radical R(x) = {to_string(res.radical)}")
    for r in res.isolated:
        cert = r.count_certificate
        print(
            f"   root #{r.isolation_id}: interval {r.interval}, "
            f"multiplicity {r.multiplicity}, "
            f"factors {[fac.index for fac in r.factors]}"
        )
        print(
            f"      cert: V(lower:{cert.lower_endpoint.side})="
            f"{cert.lower_endpoint.variation} "
            f"signs={cert.lower_endpoint.signs} | "
            f"V(upper:{cert.upper_endpoint.side})="
            f"{cert.upper_endpoint.variation} "
            f"signs={cert.upper_endpoint.signs} -> count={cert.count}"
        )
    for p in res.pending:
        print(f"   PENDING {p.interval}: certified count = {p.count}")
    if not res.isolated and not res.pending:
        print("   no real roots")
    print(f"   independent verifier: {verify_isolation(res)}")
    print()


def main():
    print("--- 1. basic isolation: x^2 - 2 ---")
    show_result(isolate(normalize([-2, 0, 1])), "x^2 - 2")

    print("--- 2. repeated roots: x^2 * (x-1)^3 ---")
    print("    (distinct roots are isolated; multiplicities listed separately)")
    show_result(
        isolate(normalize([0, 0, -1, 3, -3, 1])), "x^2 (x-1)^3"
    )

    print("--- 3. no real roots: x^2 + 1 ---")
    show_result(isolate(normalize([1, 0, 1])), "x^2 + 1")

    print("--- 4. exact endpoint-root counting for x*(x-1) ---")
    chain = sturm_chain(normalize([0, -1, 1]))
    for lo_c, hi_c in [(True, True), (False, False), (True, False),
                       (False, True)]:
        n = count_distinct_roots(chain, F(0), F(1), lo_c, hi_c)
        lb = "[" if lo_c else "("
        rb = "]" if hi_c else ")"
        print(f"   roots of x(x-1) in {lb}0, 1{rb} = {n}")
    res = isolate(
        normalize([0, -1, 1]),
        seeds=[Interval(F(0), F(1), True, True)],
        seeds_cover_all_roots=True,
    )
    show_result(res, "isolation seeded with the closed interval [0, 1]")

    print("--- 5. budget interruption and resume: (x-1)(x-2)(x-3)(x-4) ---")
    f4 = normalize([24, -50, 35, -10, 1])
    part = isolate(f4, budget=1)
    show_result(part, "budget = 1 split")
    done = resume_isolation(part)
    show_result(done, "resumed with unlimited budget")

    print("--- 6. revision: repeated root splits ((x-1)^2 -> (x-1)(x-2)) ---")
    rep = revise_polynomial(
        isolate(normalize([1, -2, 1])), normalize([2, -3, 1])
    )
    for ru in rep.reused:
        print(
            f"   reused old interval {ru.interval} (old roots "
            f"{ru.old_root_ids}): certified new count = {ru.new_count}"
        )
    for g in rep.groups:
        print(f"   group {g.kind}: old{g.old_ids} new{g.new_ids} -- {g.note}")
    print(f"   independent verifier: {verify_revision(rep)}")
    print()

    print("--- 7. revision: irrational root splits ---")
    print("    x^2 - 2  ->  x^2 - 2x + 9999/10000 (roots 1 +/- 1/100)")
    rep2 = revise_polynomial(
        isolate(normalize([-2, 0, 1])),
        normalize([F(9999, 10000), -2, 1]),
    )
    for ru in rep2.reused:
        print(
            f"   reused old interval {ru.interval}: certified new count = "
            f"{ru.new_count}"
        )
    for g in rep2.groups:
        print(f"   group {g.kind}: old{g.old_ids} new{g.new_ids}")
    print(f"   independent verifier: {verify_revision(rep2)}")
    print()

    print("--- 8. revision: two roots merge (reverse direction) ---")
    rep3 = revise_polynomial(
        isolate(normalize([F(9999, 10000), -2, 1])),
        normalize([-2, 0, 1]),
    )
    for g in rep3.groups:
        print(f"   group {g.kind}: old{g.old_ids} new{g.new_ids}")
    print(f"   independent verifier: {verify_revision(rep3)}")
    print()
    print("Note: identities are never guessed from interval-center")
    print("distances; only certified containment/counts create links, and")
    print("splits/merges are reported as groups, not as fake 1-1 matches.")


if __name__ == "__main__":
    main()
