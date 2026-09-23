"""有理数域 Q 上的单变量多项式精确运算（仅使用标准库 fractions）。

多项式表示：list[Fraction]，下标即次数，无尾部零；零多项式为 []。
所有运算均为有理数精确计算，不调用任何符号代数库。
"""
from __future__ import annotations

from fractions import Fraction
from math import lcm


def poly(coeffs):
    """由系数序列（升次）构造规范化多项式。"""
    return _trim([Fraction(c) for c in coeffs])


def _trim(p):
    p = list(p)
    while p and p[-1] == 0:
        p.pop()
    return p


def is_zero(p):
    return len(p) == 0


def is_one(p):
    return len(p) == 1 and p[0] == 1


def degree(p):
    """零多项式次数定义为 -1。"""
    return len(p) - 1


def leading_coeff(p):
    if is_zero(p):
        raise ValueError("零多项式没有首项系数")
    return p[-1]


def add(a, b):
    n = max(len(a), len(b))
    return _trim([
        (a[i] if i < len(a) else Fraction(0)) + (b[i] if i < len(b) else Fraction(0))
        for i in range(n)
    ])


def neg(a):
    return [-c for c in a]


def sub(a, b):
    return add(a, neg(b))


def scale(a, c):
    c = Fraction(c)
    return _trim([c * x for x in a])


def mul(a, b):
    if is_zero(a) or is_zero(b):
        return []
    out = [Fraction(0)] * (len(a) + len(b) - 1)
    for i, ai in enumerate(a):
        if ai == 0:
            continue
        for j, bj in enumerate(b):
            if bj:
                out[i + j] += ai * bj
    return _trim(out)


def divmod_poly(a, b):
    """带余除法：a = q*b + r，deg(r) < deg(b)。"""
    if is_zero(b):
        raise ZeroDivisionError("多项式除以零多项式")
    r = list(a)
    q = [Fraction(0)] * max(1, len(a) - len(b) + 1)
    while r and len(r) >= len(b):
        k = len(r) - len(b)
        c = r[-1] / b[-1]
        q[k] = c
        for j in range(len(b)):
            r[k + j] -= c * b[j]
        while r and r[-1] == 0:
            r.pop()
    return _trim(q), r


def div_exact(a, b):
    q, r = divmod_poly(a, b)
    if r:
        raise ArithmeticError(f"整除失败，余式非零：{fmt_poly(a)} / {fmt_poly(b)}")
    return q


def gcd(a, b):
    """首一最大公因子；gcd(0, 0) 约定为 0。"""
    while b:
        _, r = divmod_poly(a, b)
        a, b = b, r
    if is_zero(a):
        return []
    return scale(a, 1 / a[-1])


def derivative(p):
    return _trim([i * p[i] for i in range(1, len(p))])


def evaluate(p, x):
    x = Fraction(x)
    acc = Fraction(0)
    for c in reversed(p):
        acc = acc * x + c
    return acc


def monic(p):
    if is_zero(p):
        raise ValueError("零多项式无法首一化")
    return scale(p, 1 / p[-1])


def squarefree_part(p):
    """p / gcd(p, p')，与 p 有相同实根但均为单根（首一）。"""
    if is_zero(p):
        raise ValueError("零多项式没有平方自由部分")
    f = monic(p)
    return div_exact(f, gcd(f, derivative(f)))


def squarefree_factorization(p):
    """Yun 算法（特征 0）。返回 (首项系数, [(首一因子, 重数), ...])。"""
    if is_zero(p):
        raise ValueError("零多项式没有平方自由分解")
    lead = p[-1]
    f = scale(p, 1 / lead)
    factors = []
    c = gcd(f, derivative(f))
    w = div_exact(f, c)
    i = 1
    while not is_one(w):
        y = gcd(w, c)
        z = div_exact(w, y)
        if not is_one(z):
            factors.append((z, i))
        w = y
        c = div_exact(c, y)
        i += 1
    if not is_one(c):  # 特征 0 下不应发生
        raise ArithmeticError("平方自由分解失败（剩余因子非 1）")
    return lead, factors


def sturm_sequence(p):
    """Sturm 序列；p 平方自由时用于精确实根计数。"""
    seq = [p, derivative(p)]
    while True:
        _, r = divmod_poly(seq[-2], seq[-1])
        if is_zero(r):
            break
        seq.append(neg(r))
    return seq


def _sign_variations(values):
    signs = [(v > 0) - (v < 0) for v in values]
    signs = [s for s in signs if s != 0]
    return sum(1 for i in range(len(signs) - 1) if signs[i] * signs[i + 1] < 0)


def count_real_roots(p, a, b):
    """p 在开闭区间 (a, b] 内的不同实根个数（Sturm 定理，精确）。"""
    a, b = Fraction(a), Fraction(b)
    if not a < b:
        raise ValueError("区间端点需满足 a < b")
    f = squarefree_part(p)
    if degree(f) <= 0:
        return 0
    seq = sturm_sequence(f)
    va = _sign_variations([evaluate(s, a) for s in seq])
    vb = _sign_variations([evaluate(s, b) for s in seq])
    return va - vb


def rational_roots(p):
    """有理根定理：精确返回 p 的全部（互异）有理根。

    整系数多项式 a_n x^n + ... + a_0 的有理根必形如 ±d/a 的约分，
    其中 d | a_0，a | a_n。纯枚举验证，无需闭式表达。
    """
    if is_zero(p):
        raise ValueError("零多项式有无穷多个根")
    scale_up = lcm(*(c.denominator for c in p)) if p else 1
    coeffs = [int(c * scale_up) for c in p]
    roots = []
    while coeffs and coeffs[0] == 0:  # 提取因子 x^k：根 0
        roots.append(Fraction(0))
        coeffs = coeffs[1:]
    if len(coeffs) <= 1:
        return roots
    a0, an = abs(coeffs[0]), abs(coeffs[-1])

    def divisors(n):
        small = [d for d in range(1, int(n**0.5) + 1) if n % d == 0]
        return small + [n // d for d in reversed(small) if d * d != n]

    int_poly = poly(coeffs)
    seen = set(roots)
    for d in divisors(a0):
        for a in divisors(an):
            for cand in (Fraction(d, a), Fraction(-d, a)):
                if cand not in seen and evaluate(int_poly, cand) == 0:
                    seen.add(cand)
                    roots.append(cand)
    return sorted(roots)


def isolate_real_roots(p, precision=Fraction(1, 10**6)):
    """隔离 p 的全部实根。

    返回排序后的列表，元素为：
      - Fraction：精确的有理根；
      - (Fraction, Fraction)：开区间，内部含唯一（无理）实根，
        区间端点为有理数且宽度不超过 precision。
    不需要任何闭式根表达。
    """
    if is_zero(p):
        raise ValueError("零多项式有无穷多个根，无法隔离")
    f = squarefree_part(p)
    if degree(f) <= 0:
        return []
    results = []
    # 先用有理根定理精确提取全部有理根；剩余因子只含无理实根
    for r in rational_roots(f):
        results.append(r)
        f = div_exact(f, [-r, 1])
    if degree(f) <= 0:
        return sorted(results)
    seq = sturm_sequence(f)

    def var(x):
        return _sign_variations([evaluate(s, x) for s in seq])

    # Cauchy 界：所有实根严格落在 (-(1+M), 1+M) 内
    m = max((abs(c) for c in f[:-1]), default=Fraction(0))
    bound = 1 + m

    def rec(lo, hi, count):
        # 不变式：开区间 (lo, hi) 内恰好有 count 个实根
        if count == 0:
            return
        if count == 1 and hi - lo <= precision:
            results.append((lo, hi))
            return
        mid = (lo + hi) / 2
        vmid = var(mid)
        if evaluate(f, mid) == 0:
            # 二分恰好命中一个有理根：精确记录，左右开区间继续
            results.append(mid)
            left = var(lo) - vmid - 1
            right = count - 1 - left
        else:
            left = var(lo) - vmid
            right = count - left
        rec(lo, mid, left)
        rec(mid, hi, right)

    rec(-bound, bound, var(-bound) - var(bound))
    return sorted(results, key=lambda it: (it[0] if isinstance(it, tuple) else it,
                                           it[1] if isinstance(it, tuple) else it))


def fmt_poly(p, var="x"):
    if is_zero(p):
        return "0"
    parts = []
    for i in range(len(p) - 1, -1, -1):
        c = p[i]
        if c == 0:
            continue
        sign = "-" if c < 0 else "+"
        mag = abs(c)
        if i == 0:
            body = str(mag)
        else:
            coef = "" if mag == 1 else f"{mag}*"
            power = var if i == 1 else f"{var}^{i}"
            body = f"{coef}{power}"
        parts.append((sign, body))
    out = ("-" if parts[0][0] == "-" else "") + parts[0][1]
    for sign, body in parts[1:]:
        out += f" {'-' if sign == '-' else '+'} {body}"
    return out
