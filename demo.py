"""Run samples for the exact rational polynomial toolkit.

Covers: division with remainder, extended Euclid with Bezout
coefficients, the subresultant PRS with per-step quotient/remainder and
Bezout certificates, coefficient budget, and pause/resume. Every result
is validated by the independent naive reference (reference.py).

Run: python3.11 demo.py
"""
from fractions import Fraction

import reference as ref
from polygcd import as_poly, divmod_poly, monic, mul, scale, xgcd
from prs import CoefficientBudget, PRSComputation


def fmt(p):
    p = as_poly(p)
    if not p:
        return "0"
    parts = []
    for i in range(len(p) - 1, -1, -1):
        c = p[i]
        if c == 0:
            continue
        sign = "-" if c < 0 else "+"
        a = abs(c)
        a = a.numerator if a.denominator == 1 else a
        if i == 0:
            body = str(a)
        elif i == 1:
            body = "x" if a == 1 else f"{a}*x"
        else:
            body = f"x^{i}" if a == 1 else f"{a}*x^{i}"
        parts.append((sign, body))
    first_sign, first_body = parts[0]
    out = ("-" if first_sign == "-" else "") + first_body
    for sign, body in parts[1:]:
        out += f" {sign} {body}"
    return out


def poly_pow(p, n):
    out = [Fraction(1)]
    for _ in range(n):
        out = mul(out, p)
    return out


def main():
    print("=" * 72)
    print("1. Division with remainder in Q[x] (exact rational arithmetic)")
    print("=" * 72)
    f = as_poly([Fraction(-5), Fraction(2), Fraction(-1), Fraction(3)])
    g = as_poly([Fraction(-1), Fraction(2)])
    q, r = divmod_poly(f, g)
    print(f"f = {fmt(f)}")
    print(f"g = {fmt(g)}")
    print(f"q = {fmt(q)}")
    print(f"r = {fmt(r)}   (deg r = {len(r) - 1 if r else -1} < deg g = 1)")
    ref.check_division(f, g, q, r)
    print("identity f = q*g + r verified by independent naive reference: OK")
    try:
        divmod_poly(f, [])
    except ZeroDivisionError as exc:
        print(f"division by zero polynomial rejected: {exc}")

    print()
    print("=" * 72)
    print("2. Extended Euclid: monic gcd + Bezout coefficients")
    print("=" * 72)
    f = as_poly([Fraction(-2), Fraction(-3), Fraction(0), Fraction(1)])
    g = as_poly([Fraction(4), Fraction(4), Fraction(1)])
    d, s, t, steps = xgcd(f, g)
    print(f"f = {fmt(f)}")
    print(f"g = {fmt(g)}")
    print(f"d = gcd(f, g) = {fmt(d)}   (monic)")
    print(f"s = {fmt(s)}")
    print(f"t = {fmt(t)}")
    ref.validate_gcd(f, g, d, s, t)
    print("Bezout s*f + t*g == d verified exactly by naive reference: OK")
    print("step log (each transformation states its domain):")
    for st in steps:
        note = st.get("equation") or st.get("note")
        print(f"  - {note}  [{st.get('domain')}]")

    print()
    print("=" * 72)
    print("3. Subresultant PRS: non-monic inputs, multiple common factors,")
    print("   per-step quotient/remainder and Bezout certificates")
    print("=" * 72)
    xm1 = as_poly([-1, 1])
    xp2 = as_poly([2, 1])
    f = scale(mul(poly_pow(xm1, 3), poly_pow(xp2, 2)), 6)   # 6(x-1)^3(x+2)^2
    g = scale(mul(poly_pow(xm1, 2), poly_pow(xp2, 3)), 4)   # 4(x-1)^2(x+2)^3
    print(f"f = {fmt(f)}   [= 6(x-1)^3(x+2)^2]")
    print(f"g = {fmt(g)}   [= 4(x-1)^2(x+2)^3]")
    comp = PRSComputation(f, g)
    comp.run()
    for st in comp.steps:
        if st["kind"] == "prs-step":
            print(f"step -> R{st['index']}:")
            print(f"  pseudo-quotient  Q = {fmt(st['pseudo_quotient'])}")
            print(f"  pseudo-remainder R = {fmt(st['pseudo_remainder'])}"
                  f"   (mult = lc(B)^(d+1) = {st['multiplier']})")
            print(f"  beta (growth control) = {st['beta']}")
            print(f"  next remainder R/beta = {fmt(st['remainder'])}")
            print(f"  Bezout certificate: s = {fmt(st['bezout_s'])},"
                  f" t = {fmt(st['bezout_t'])}")
            for dom in st["domains"]:
                print(f"  domain: {dom}")
        else:
            print(f"{st['kind']}: {st['note']}  [{st['domain']}]")
    d, s, t = comp.result
    print(f"gcd = {fmt(d)}   (monic; expected (x-1)^2(x+2)^2"
          f" = {fmt(monic(mul(poly_pow(xm1, 2), poly_pow(xp2, 2))))})")
    print(f"Bezout: s = {fmt(s)}")
    print(f"        t = {fmt(t)}")
    ref.validate_prs_steps(f, g, comp.steps, d, s, t)
    print("all step identities, degree descent, certificates and gcd"
          " validated by naive reference: OK")

    print()
    print("=" * 72)
    print("4. Coefficient budget: exceeding it keeps the unfinished state")
    print("=" * 72)
    f2 = as_poly([1, 2, 0, 1])
    g2 = as_poly([3, -5, 0, 12])
    comp = PRSComputation(f2, g2, budget=CoefficientBudget(2))
    status = comp.run()
    print(f"tiny budget (2 digits): status = {status}, result = {comp.result}")
    status = comp.resume(budget=CoefficientBudget(10 ** 6))
    print(f"resumed with larger budget: status = {status}")
    print(f"gcd = {fmt(comp.result[0])}")
    ref.validate_gcd(f2, g2, *comp.result)
    print("resumed result validated by naive reference: OK")

    print()
    print("=" * 72)
    print("5. Pause / resume and snapshot restore")
    print("=" * 72)
    comp = PRSComputation(f, g)
    statuses = []
    while comp.status != PRSComputation.DONE:
        statuses.append(comp.run(max_steps=1))
        if comp.status == PRSComputation.PAUSED:
            comp.resume()
    print(f"one-step-at-a-time statuses: {statuses}")
    comp2 = PRSComputation(f, g)
    comp2.run(max_steps=1)
    snap = comp2.snapshot()
    restored = PRSComputation.from_snapshot(snap)
    restored.resume()
    print(f"snapshot after 1 step -> restored and resumed: {restored.status},"
          f" gcd = {fmt(restored.result[0])}")
    assert restored.result == comp.result
    print("pause/resume and snapshot restore agree with straight run: OK")

    print()
    print("ALL DEMO CHECKS PASSED")


if __name__ == "__main__":
    main()
