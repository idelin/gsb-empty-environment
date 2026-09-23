"""polysub 运行样例：同时代换 / 零消项 / 精确有理数 / 项数预算。

运行：python3.11 demo.py
"""

from fractions import Fraction


from polysub import BudgetExceeded, Poly

x = Poly.var("x")
y = Poly.var("y")
a = Poly.var("a")
b = Poly.var("b")
c = Poly.var("c")


def show(title):
    print(f"\n=== {title} ===")


show("1. 同时代换：x→y、y→x 是交换，不是全部变成 x")
p = x**2 + x * y + 2
print("p                      =", p)
print("p[x→y, y→x]（同时）    =", p.substitute({"x": y, "y": x}))
polluted = p.substitute({"x": y}).substitute({"y": x})
print("p 先 x→y 再 y→x（顺序）=", polluted, " ← 顺序代换的污染结果")

show("2. 替换式中的变量不会被二次代换")
q = x - y
print("q                      =", q)
print("q[x→y+1, y→x+1]        =", q.substitute({"x": y + 1, "y": x + 1}))

show("3. 代换零消项")
r = x**2 + x * y + y + 3
print("r                      =", r)
print("r[x→0]                 =", r.substitute({"x": 0}), " ← 含 x 的项消去，常数项保留")

show("4. 精确有理数（无浮点误差）")
s = 3 * x**2
print("3x^2 在 x=1/3 处       =", s.substitute({"x": Fraction(1, 3)}))
print("(x + 1/2)^2            =", (x + Fraction(1, 2)) ** 2)

show("5. 项数预算：估计膨胀，超预算保留未展开形式")
big = x**5
deferred = big.substitute({"x": a + b}, budget=5)
print("预算 5：", deferred, " ← 未展开的 Expr，不是截断假等式")
try:
    deferred.expand(budget=5)
except BudgetExceeded as e:
    print("expand(budget=5) 抛出:", e)
done = big.substitute({"x": a + b}, budget=6)
print("预算 6：", done)
print("事后充足预算展开       =", deferred.expand())
print("与直接展开相等：", deferred.expand() == done)

show("6. 更大的膨胀估计")
u = x**10
est_deferred = u.substitute({"x": a + b + c}, budget=50)
print("x^10[x→a+b+c]，预算 50：", est_deferred)
expanded = (a + b + c) ** 10
print("估计项数 =", est_deferred.term_estimate(), "；实际展开项数 =", expanded.num_terms)
