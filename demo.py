"""Demo: exact rational polynomial division, extended Euclid, and the
subresultant PRS with pause/resume and a coefficient budget."""
from fractions import Fraction

from polyq import poly, mul, divmod_q, extended_gcd, PRSComputer, to_str
from polyq import reference as R


def show(title):
    print(f"\n{'=' * 72}\n{title}\n{'=' * 72}")


def pmul(*ps):
    out = [Fraction(1)]
    for p in ps:
        out = mul(out, p)
    return out


# --- 1. division with remainder over Q[x] ----------------------------------
show("1. Exact division with remainder over Q[x]")
a = poly([1, "-1/2", 2, "3/4"])     # 3/4 x^3 + 2 x^2 - 1/2 x + 1
b = poly([-3, 2])                   # 2x - 3
q, r = divmod_q(a, b)
print(f"a = {to_str(a)}\nb = {to_str(b)}")
print(f"q = {to_str(q)}\nr = {to_str(r)}   (deg r = {-1 if not r else len(r)-1} < deg b = 1)")
R.check_division(a, b, q, r)
print("independent check: a == q*b + r  [OK]")
try:
    divmod_q(a, [])
except ZeroDivisionError as exc:
    print(f"division by zero polynomial rejected: {exc}")

# --- 2. extended Euclid with Bezout certificate ----------------------------
show("2. Extended Euclidean algorithm (non-monic inputs, multiple common factor)")
a = pmul(poly([-2, 4]), poly([-1, 1]), poly([-1, 1]), poly([3, 1]))   # 4(x-1/2)(x-1)^2(x+3)
b = pmul(poly([-6, 3]), poly([-1, 1]), poly([3, 1]), poly([3, 1]))    # 3(x-2)(x-1)(x+3)^2
print(f"a = {to_str(a)}\nb = {to_str(b)}")
g, s, t = extended_gcd(a, b)
print(f"g = {to_str(g)}   (monic gcd)")
print(f"s = {to_str(s)}\nt = {to_str(t)}")
R.check_gcd(a, b, g, s, t)
print("independent check: s*a + t*b == g, g | a, g | b, g monic  [OK]")
print("(maximality follows from the Bezout identity: every common divisor divides g)")

# --- 3. subresultant PRS with per-step certificates ------------------------
show("3. Subresultant PRS: per-step quotient/remainder + Bezout certificates")
c = PRSComputer(a, b)
print(f"status after run(): {c.run()}")
for st in c.steps:
    print(f"\nstep {st.index}: delta-deg transition")
    print(f"  dividend        = {to_str(st.dividend)}")
    print(f"  divisor         = {to_str(st.divisor)}")
    print(f"  pseudo-quotient = {to_str(st.pseudo_quotient)}")
    print(f"  pseudo-rem      = {to_str(st.pseudo_remainder)}")
    print(f"  beta            = {st.beta}")
    print(f"  remainder       = {to_str(st.remainder)}")
    print(f"  bezout s        = {to_str(st.bezout_s)}")
    print(f"  bezout t        = {to_str(st.bezout_t)}")
    print(f"  domain note     = {st.note}")
R.check_prs_steps(c.A0, c.B0, c.steps)
print(f"\ngcd from PRS = {to_str(c.gcd)}")
R.check_gcd(a, b, c.gcd, *c.bezout)
print("independent check of every step + final gcd certificate  [OK]")

# --- 4. pause / resume with a coefficient budget ---------------------------
show("4. Pause/resume and coefficient budget")
big_a = pmul(poly([-1, 1]), poly([-1, 1]), poly([2, 1]), poly([1, 0, 1]), poly([3, 1]))
big_b = pmul(poly([-1, 1]), poly([3, 1]), poly([-2, 1]), poly([5, 1]), poly([7, 1]))
c = PRSComputer(big_a, big_b, budget=120)
status = c.run()
print(f"tiny budget=120 -> status: {status} (gcd is None: {c.gcd is None})")
c.budget = 10 ** 6
print(f"budget raised, resume -> status: {c.run()}")
print(f"gcd = {to_str(c.gcd)}")
R.check_gcd(big_a, big_b, c.gcd, *c.bezout)
print("independent check after resume  [OK]")

c = PRSComputer(big_a, big_b)
print(f"\npause every step: ", end="")
while True:
    st = c.run(max_steps=1)
    print(st, "-> ", end="")
    if st == "completed":
        break
print("done")
import json
blob = json.dumps(c.checkpoint())
c2 = PRSComputer.restore(json.loads(blob))
print(f"checkpoint JSON round-trip: {len(blob)} bytes, restored gcd matches: "
      f"{c2.gcd == c.gcd}")

# --- 5. zero-polynomial boundaries -----------------------------------------
show("5. Zero-polynomial boundaries")
for a, b in [([], []), (poly([2, 4, 6]), []), ([], poly([3, 0, -6]))]:
    g, s, t = extended_gcd(a, b)
    R.check_gcd(a, b, g, s, t)
    print(f"gcd({to_str(a)}, {to_str(b)}) = {to_str(g)}  [checked]")
