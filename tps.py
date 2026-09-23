"""截断有理幂级数（Truncated rational Power Series）。

系数为精确有理数（fractions.Fraction），只使用标准库。
每个级数带有显式的截断阶数 N，表示在模 x^N 意义下保留
x^0 .. x^(N-1) 的系数。所有运算的阶数传播规则在各方法的
docstring 中明确说明，并标注该变换的适用域。
"""

from fractions import Fraction


class TPS:
    """截断有理幂级数 f(x) = sum_{k=0}^{N-1} a_k x^k  (mod x^N)。

    适用域：任意有理系数、任意正整数截断阶数 N。
    构造时系数被规范化为长度恰为 N（不足补 0，超出截断）。
    """

    def __init__(self, coeffs, order=None):
        coeffs = tuple(Fraction(c) for c in coeffs)
        if order is None:
            order = len(coeffs)
        if order <= 0:
            raise ValueError("截断阶数必须为正整数")
        if len(coeffs) < order:
            coeffs = coeffs + (Fraction(0),) * (order - len(coeffs))
        else:
            coeffs = coeffs[:order]
        self._coeffs = coeffs
        self._order = order

    # ---- 基本访问 ----

    @property
    def order(self):
        """截断阶数 N（保留 x^0 .. x^(N-1)）。"""
        return self._order

    @property
    def coeffs(self):
        """长度恰为 order 的系数元组，元素为 Fraction。"""
        return self._coeffs

    def __getitem__(self, k):
        return self._coeffs[k]

    def __repr__(self):
        return f"TPS({list(self._coeffs)!r}, order={self._order})"

    def __eq__(self, other):
        return (
            isinstance(other, TPS)
            and self._order == other._order
            and self._coeffs == other._coeffs
        )

    # ---- 加法 / 减法 ----

    def add(self, other):
        """返回 self + other。

        适用域：两个级数截断阶数必须相同；结果阶数 = 该共同阶数。
        阶数不一致时无法确定结果的截断语义，直接抛出 ValueError，
        不做隐式阶数传播。
        """
        self._require_same_order(other)
        return TPS(
            [a + b for a, b in zip(self._coeffs, other._coeffs)], self._order
        )

    def sub(self, other):
        """返回 self - other。适用域与阶数规则同 add。"""
        self._require_same_order(other)
        return TPS(
            [a - b for a, b in zip(self._coeffs, other._coeffs)], self._order
        )

    __add__ = add
    __sub__ = sub

    def _require_same_order(self, other):
        if not isinstance(other, TPS):
            raise TypeError("运算对象必须是 TPS")
        if self._order != other._order:
            raise ValueError(
                f"截断阶数不一致: {self._order} != {other._order}"
            )

    # ---- 乘法 ----

    def mul(self, other, order=None):
        """返回 self * other，只保留到指定截断阶数。

        适用域：任意两个 TPS（阶数可以不同）。
        阶数传播：结果阶数 = order；order 缺省时取
        min(self.order, other.order)——两个因子都精确已知的最高阶。
        超出结果阶数的项被丢弃，绝不保留。
        """
        if not isinstance(other, TPS):
            raise TypeError("运算对象必须是 TPS")
        if order is None:
            order = min(self._order, other._order)
        if order <= 0:
            raise ValueError("截断阶数必须为正整数")
        out = [Fraction(0)] * order
        for i, a in enumerate(self._coeffs):
            if i >= order:
                break
            if a == 0:
                continue
            for j, b in enumerate(other._coeffs):
                k = i + j
                if k >= order:
                    break
                out[k] += a * b
        return TPS(out, order)

    __mul__ = mul

    # ---- 乘法逆 ----

    def inverse(self, order=None):
        """返回乘法逆 g，使得 self * g == 1 (mod x^order)。

        适用域：仅当常数项 a_0 != 0 时逆存在（形式幂级数环中
        单位恰为常数项非零的级数）。a_0 == 0 时抛出 ValueError，
        绝不返回零系数或其他伪结果蒙混。
        阶数传播：结果阶数 = order；缺省时取 self.order。
        """
        if self._coeffs[0] == 0:
            raise ValueError(
                "常数项为 0 的幂级数没有乘法逆（适用域：a_0 != 0）"
            )
        if order is None:
            order = self._order
        if order <= 0:
            raise ValueError("截断阶数必须为正整数")
        a0 = self._coeffs[0]
        g = [Fraction(0)] * order
        g[0] = 1 / a0
        # 递推：g_n = -(1/a_0) * sum_{i=1..n} a_i * g_{n-i}
        for n in range(1, order):
            s = Fraction(0)
            for i in range(1, n + 1):
                a_i = self._coeffs[i] if i < self._order else Fraction(0)
                s += a_i * g[n - i]
            g[n] = -s / a0
        return TPS(g, order)

    # ---- 复合 ----

    def compose(self, inner, order=None):
        """返回复合 f(inner(x))，即 self 作为外层级数。

        适用域：内层级数 inner 的常数项必须为 0，否则代入后
        各次幂对低阶项均有贡献，截断复合在形式幂级数意义下
        不收敛（除非外层是多项式——本实现不做该特例判断，
        一律要求 inner[0] == 0，违反时抛出 ValueError）。
        阶数传播：结果阶数 = order；缺省时取
        min(self.order, inner.order)。计算用 Horner 法，
        每一步中间结果都截断到结果阶数，因 inner 无常数项，
        inner^k 的最低次 >= k，截断不影响精度。
        """
        if not isinstance(inner, TPS):
            raise TypeError("内层级数必须是 TPS")
        if inner._coeffs[0] != 0:
            raise ValueError(
                "复合要求内层级数常数项为 0（适用域：inner[0] == 0）"
            )
        if order is None:
            order = min(self._order, inner._order)
        if order <= 0:
            raise ValueError("截断阶数必须为正整数")
        inner_t = TPS(inner._coeffs, order)  # 内层截断到结果阶数
        result = TPS([Fraction(0)], order)
        for a_k in reversed(self._coeffs):
            result = result.mul(inner_t, order).add(
                TPS([a_k], order)
            )
        return result

    # ---- 工具构造 ----

    @staticmethod
    def zero(order):
        """零级数，截断阶数为 order。"""
        return TPS([Fraction(0)], order)

    @staticmethod
    def one(order):
        """常数级数 1，截断阶数为 order。"""
        return TPS([Fraction(1)], order)
