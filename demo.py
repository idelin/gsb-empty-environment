"""运行样例：截断有理幂级数的加、乘、逆、复合。"""

from fractions import Fraction
from tps import TPS

N = 5

f = TPS([1, Fraction(1, 2), Fraction(-1, 3), 0, 2], order=N)
g = TPS([2, -1, Fraction(1, 4), 0, 0], order=N)

print("f =", f)
print("g =", g)
print("f + g =", f + g)
print("f * g (mod x^%d) =" % N, f * g)
print("f * g 只保留到 x^2:", f.mul(g, order=3))

finv = f.inverse()
print("f 的逆 f^-1 =", finv)
print("f * f^-1 (mod x^%d) =" % N, f.mul(finv, order=N))

inner = TPS([0, 1, Fraction(1, 2)], order=N)  # 常数项为 0，合法
print("f ∘ inner =", f.compose(inner))

for bad, op in [
    (TPS([0, 1, 1], order=3), "inverse"),
    (TPS([1, 1], order=3), "compose-inner"),
]:
    try:
        if op == "inverse":
            bad.inverse()
        else:
            f.compose(bad)
    except ValueError as exc:
        print(f"非法{op}被拒绝: {exc}")
