"""精确有理插值：Newton 差商 / 规范幂基 / Hermite 重复节点 / 增量观测管理。

只使用 Python 标准库（fractions.Fraction 做精确有理运算），不调用任何
符号代数库。模块内的每一类变换都在其文档字符串中注明"适用域"。

核心对象
--------
* :class:`Poly`：规范幂基多项式，系数按升幂排列（a0 + a1 x + ...）。
* :class:`Interpolator`：以观测 (x, k) -> v 表示"在 x 处的 k 阶导数值为 v"
  （k=0 即函数值）。内部维护汇合差商表（confluent divided differences），
  支持增量加入、撤回任意位置的观测以及重复节点诊断。
* :func:`linear_solve`：独立的线性方程（增广矩阵）精确求解器，用于对插值
  结果做参考核验，秩亏且不相容时给出见证行（矛盾的线性组合）。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from math import factorial
from typing import Callable, Dict, List, Optional, Sequence, Tuple

Rational = Fraction


class InterpolationError(ValueError):
    """插值数据在结构或语义上非法的基类。"""


class DuplicateConflictError(InterpolationError):
    """同一约束 (x, k) 被赋了两个不同的值（重复 x 不同 y 冲突）。

    属性 ``x``、``order``、``existing``、``incoming`` 记录冲突细节。
    """

    def __init__(self, x: Rational, order: int,
                 existing: Rational, incoming: Rational) -> None:
        self.x = x
        self.order = order
        self.existing = existing
        self.incoming = incoming
        super().__init__(
            f"冲突：节点 x={_fmt(x)} 的 {order} 阶导数已为 {_fmt(existing)}，"
            f"不能再赋 {_fmt(incoming)}")


class StructureError(InterpolationError):
    """加入低阶导数缺失的高阶约束等结构性错误（Hermite 导数必须从 0 阶连续）。"""


class UnknownObservationError(InterpolationError):
    """撤回一个不存在的观测。"""


# ---------------------------------------------------------------------------
# 规范幂基多项式
# ---------------------------------------------------------------------------


def _fmt(z: "Rational | Poly") -> str:
    """紧凑的有理数/多项式显示格式。"""
    if isinstance(z, Poly):
        return str(z)
    if isinstance(z, int):
        return str(z)
    if z.denominator == 1:
        return str(z.numerator)
    return f"{z.numerator}/{z.denominator}"


class Poly:
    """有理系数多项式，规范幂基表示：``coeffs[i]`` 是 ``x**i`` 的系数。

    适用域：任意特征 0 的系数域（此处为 Q）。构造时自动去除最高次零系数，
    保证表示唯一，因此两个多项式相等可直接比较其 ``coeffs``。
    """

    __slots__ = ("coeffs",)

    def __init__(self, coeffs: Sequence[Rational] = ()) -> None:
        c = [Fraction(a) for a in coeffs]
        while len(c) > 1 and c[-1] == 0:
            c.pop()
        if not c:
            c = [Fraction(0)]
        self.coeffs: List[Fraction] = c

    # --- 基本性质 ---------------------------------------------------------
    @property
    def degree(self) -> int:
        return 0 if (len(self.coeffs) == 1 and self.coeffs[0] == 0) else len(self.coeffs) - 1

    def eval(self, x: Rational) -> Fraction:
        """Horner 法精确求值。适用域：x 为任意有理数。"""
        x = Fraction(x)
        acc = Fraction(0)
        for a in reversed(self.coeffs):
            acc = acc * x + a
        return acc

    def derivative(self, k: int = 1) -> "Poly":
        """返回 k 阶形式导数多项式。适用域：k>=0，特征 0 域。"""
        if k < 0:
            raise ValueError("导数阶数必须非负")
        c = self.coeffs
        for _ in range(k):
            c = [factorial(j + 1) // factorial(j) * c[j + 1]
                 for j in range(len(c) - 1)] if len(c) > 1 else []
            if not c:
                c = [Fraction(0)]
        return Poly(c)

    def eval_derivative(self, x: Rational, k: int) -> Fraction:
        """p^(k)(x)，先求形式导数再 Horner 求值。"""
        return self.derivative(k).eval(x)

    # --- 运算 -------------------------------------------------------------
    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Poly):
            return NotImplemented
        return self.coeffs == other.coeffs

    def __hash__(self) -> int:
        return hash(tuple(self.coeffs))

    def __add__(self, other: "Poly") -> "Poly":
        n = max(len(self.coeffs), len(other.coeffs))
        return Poly([(self.coeffs[i] if i < len(self.coeffs) else 0)
                     + (other.coeffs[i] if i < len(other.coeffs) else 0)
                     for i in range(n)])

    def scale(self, a: Rational) -> "Poly":
        """数乘。适用域：任意有理数 a。"""
        return Poly([a * c for c in self.coeffs])

    def mul_by_linear(self, root: Rational) -> "Poly":
        """乘以 (x - root)。适用域：root 为任意有理数（Newton 基递推用）。"""
        root = Fraction(root)
        old = self.coeffs
        new: List[Fraction] = [Fraction(0)] * (len(old) + 1)
        for i, a in enumerate(old):
            new[i] -= a * root
            new[i + 1] += a
        return Poly(new)

    @classmethod
    def from_newton(cls, nodes: Sequence[Rational],
                    coeffs: Sequence[Rational]) -> "Poly":
        """由 Newton 形式 ``sum c_j prod_{i<j}(x-x_i)`` 展开为规范幂基。

        适用域：节点可任意排序、允许重复（重复节点只出现在 Hermite 汇合
        表示中；本函数对重复节点同样按代数乘法处理，结果仍正确）。
        """
        p = cls([0])
        basis = cls([1])
        for j, c in enumerate(coeffs):
            if j > 0:
                basis = basis.mul_by_linear(nodes[j - 1])
            p = p + basis.scale(Fraction(c))
        return p

    def __str__(self) -> str:
        if self.degree == 0 and self.coeffs[0] == 0:
            return "0"
        terms: List[str] = []
        for i, a in enumerate(self.coeffs):
            if a == 0:
                continue
            if i == 0:
                terms.append(_fmt(a))
            else:
                coef = "" if a == 1 else ("-" if a == -1 else _fmt(a))
                terms.append(f"{coef}x" if i == 1 else f"{coef}x^{i}")
        out = terms[0].replace("+", "")
        for t in terms[1:]:
            out += f" - {t[1:]}" if t.startswith("-") else f" + {t}"
        return out


# ---------------------------------------------------------------------------
# 分子/分母位长统计
# ---------------------------------------------------------------------------


class BitTracker:
    """记录各阶段有理数分子/分母的最大二进制位长（整数绝对值的 bit_length）。"""

    def __init__(self) -> None:
        self.stages: Dict[str, Tuple[int, int]] = {}

    def record(self, stage: str, values) -> None:
        nb = db = 0
        for v in values:
            if isinstance(v, Poly):
                nums = [a.numerator for a in v.coeffs]
                dens = [a.denominator for a in v.coeffs]
            elif isinstance(v, (list, tuple)):
                nums, dens = [], []
                for w in v:
                    nums.append(getattr(w, "numerator", w))
                    dens.append(getattr(w, "denominator", 1))
            else:
                nums = [getattr(v, "numerator", v)]
                dens = [getattr(v, "denominator", 1)]
            nb = max([nb] + [abs(int(n)).bit_length() for n in nums])
            db = max([db] + [abs(int(d)).bit_length() for d in dens])
        prev = self.stages.get(stage, (0, 0))
        self.stages[stage] = (max(prev[0], nb), max(prev[1], db))

    def report(self) -> Dict[str, Tuple[int, int]]:
        return dict(self.stages)


# ---------------------------------------------------------------------------
# 独立线性方程精确求解（高斯-若尔当消元，带见证行追踪）
# ---------------------------------------------------------------------------


@dataclass
class SolveResult:
    consistent: bool
    solution: Optional[List[Fraction]] = None
    """一致时的一个特解（自由变量取 0）。"""
    witness: Optional[List[Fraction]] = None
    """不相容时的见证：原方程行的线性组合，使左边为 0、右边非 0。"""
    rank: int = 0


def linear_solve(A: Sequence[Sequence[Rational]],
                 b: Sequence[Rational]) -> SolveResult:
    """对线性方程组 ``A x = b`` 做精确高斯-若尔当消元。

    适用域：A 为任意 m*n 的有理矩阵；m 与 n 不必相等（可超定/欠定）。
    超定且相容时返回一个特解；不相容时返回见证向量 w：w·A=0 但 w·b!=0，
    即"条件不相容"的证书。追踪方法是把每行初始化为单位行 e_i，消元过程中
    与矩阵行做完全相同的行组合，矛盾行 0=c!=0 对应的记录即为见证。
    """
    m = len(A)
    n = len(A[0]) if m else 0
    rows: List[List[Fraction]] = [[Fraction(v) for v in row] for row in A]
    rhs: List[Fraction] = [Fraction(v) for v in b]
    provenance: List[List[Fraction]] = [
        [Fraction(1 if i == j else 0) for j in range(m)] for i in range(m)]

    pivot_row = 0
    rank = 0
    for col in range(n):
        cand = next((r for r in range(pivot_row, m) if rows[r][col] != 0), None)
        if cand is None:
            continue
        rows[pivot_row], rows[cand] = rows[cand], rows[pivot_row]
        rhs[pivot_row], rhs[cand] = rhs[cand], rhs[pivot_row]
        provenance[pivot_row], provenance[cand] = provenance[cand], provenance[pivot_row]

        piv = rows[pivot_row][col]
        rows[pivot_row] = [v / piv for v in rows[pivot_row]]
        rhs[pivot_row] /= piv
        provenance[pivot_row] = [v / piv for v in provenance[pivot_row]]
        for r in range(m):
            if r == pivot_row or rows[r][col] == 0:
                continue
            factor = rows[r][col]
            rows[r] = [a - factor * c for a, c in zip(rows[r], rows[pivot_row])]
            rhs[r] -= factor * rhs[pivot_row]
            provenance[r] = [a - factor * c
                             for a, c in zip(provenance[r], provenance[pivot_row])]
        pivot_row += 1
        rank += 1

    for r in range(rank, m):
        if rhs[r] != 0:
            return SolveResult(False, None, provenance[r], rank)

    solution = [Fraction(0)] * n
    pivot_cols: List[int] = []
    pr = 0
    for col in range(n):
        if pr < rank and rows[pr][col] != 0:
            pivot_cols.append(col)
            pr += 1
    for r, col in enumerate(pivot_cols):
        solution[col] = rhs[r]
    return SolveResult(True, solution, None, rank)


# ---------------------------------------------------------------------------
# 增量插值器：汇合差商（Newton 基）+ 观测管理
# ---------------------------------------------------------------------------


def _dd_ddiv(observations, seq: List[Tuple[Fraction, int]],
             tab: List[List[Optional[Fraction]]], i: int, j: int) -> Fraction:
    """计算差商表表项 tab[i][j] = f[z_i,...,z_{i+j}]。

    适用域：seq 中同一节点 x 的重复出现必须连续，且块内第 t 次出现携带
    0..t 阶导数值（汇合差商）。两端点不同时为普通差商；相同时为汇合项，
    等于该节点处 (j) 阶导数除以 j!，其中 j 是重复跨度。
    """
    xi, ki = seq[i]
    xj, _ = seq[i + j]
    if xi == xj:
        return observations[(xi, j)] / factorial(j)
    return (tab[i + 1][j - 1] - tab[i][j - 1]) / (xj - xi)


@dataclass
class Verification:
    ok: bool
    residuals: Dict[Tuple[Fraction, int], Fraction]
    degree: int
    expected_degree: int


class Interpolator:
    """管理全部观测并构造 Hermite 型 Newton 插值多项式。

    内部序列 ``seq`` 按"节点块"排列：每个不同 x 是一个连续块，块内依次
    携带 0,1,...,m_x 阶导数。``tab[i][j]`` 是汇合差商，Newton 系数为
    对角线起点 ``tab[0][j]``。

    增量复用：任何加/撤操作只影响从位置 p 开始的表项；``_reload(p)``
    保留上三角中 i+j < p 的旧表项，仅重算 i+j >= p 的子三角——因此撤回
    中间节点后"不能只删最后一个系数"，而是复用前缀并重建后缀。
    """

    def __init__(self) -> None:
        self.observations: Dict[Tuple[Fraction, int], Fraction] = {}
        self.seq: List[Tuple[Fraction, int]] = []
        self.tab: List[List[Optional[Fraction]]] = []
        self.bits = BitTracker()

    # ------------------------------------------------------------------
    # 观测的增量加入
    # ------------------------------------------------------------------
    def observe(self, x, y, derivative_order: int = 0) -> None:
        """加入观测：x 处 derivative_order 阶导数等于 y。

        规则（适用域为 Hermite 汇合条件）：
        * (x,0) 重复加入相同 y：幂等，无操作；不同 y：抛 DuplicateConflictError。
        * 任意 (x,k) 同理：同值幂等，异值冲突。
        * 高阶导数必须等低阶（0..k-1 阶）都已给出，否则 StructureError。
        * 新节点块追加到序列末尾；给已有块补高一阶导数若该块在末尾则
          原位增量插入，否则插入后仅重建后缀子三角。
        """
        x = Fraction(x)
        y = Fraction(y)
        k = int(derivative_order)
        if k < 0:
            raise StructureError("导数阶数必须非负")
        key = (x, k)
        if key in self.observations:
            old = self.observations[key]
            if old == y:
                return  # 重复同值：幂等
            raise DuplicateConflictError(x, k, old, y)

        block_start = next((i for i, (z, _) in enumerate(self.seq) if z == x), None)
        if block_start is None:
            if k != 0:
                raise StructureError(
                    f"节点 x={_fmt(x)} 缺少 0 阶函数值，不能先给 {k} 阶导数")
            insert_at = len(self.seq)
        else:
            block_end = block_start + 1
            while block_end < len(self.seq) and self.seq[block_end][0] == x:
                block_end += 1
            present = {kk for (z, kk) in self.seq[block_start:block_end] if z == x}
            if k != block_end - block_start:
                raise StructureError(
                    f"节点 x={_fmt(x)} 的导数阶数必须连续：已有 {sorted(present)}，"
                    f"下一个应为 {block_end - block_start} 阶，而不是 {k} 阶")
            if block_end != len(self.seq):
                raise StructureError(
                    f"节点 x={_fmt(x)} 的块必须连续：它后面已经存在别的节点块。"
                    "请在加入其它节点之前连续给出同一节点的全部导数，"
                    "或新建 Interpolator 改变节点顺序（最终规范幂基与顺序无关）")
            insert_at = block_end

        self.observations[key] = y
        self.seq.insert(insert_at, (x, k))
        self._reload(insert_at)

    # ------------------------------------------------------------------
    # 差商表（复用前缀，只重建后缀）
    # ------------------------------------------------------------------
    def _reload(self, p: int) -> None:
        """使差商表与新 seq 一致，复用所有 i+j < p 的旧表项。

        适用域：p 是发生结构变化的最小下标；p 之前的 (z,k) 序列不变，
        故旧差商 f[z_i..z_{i+j}]（i+j<p）数学上完全可复用。
        """
        n = len(self.seq)
        old = self.tab
        tab: List[List[Optional[Fraction]]] = [
            [None] * (n - i) for i in range(n)]
        # 必须按列（阶数 j）升序、列内按 i 升序填充：
        # tab[i][j] 依赖 tab[i][j-1] 与 tab[i+1][j-1]（都是左一列）。
        for j in range(n):
            for i in range(n - j):
                if old and i < len(old) and j < len(old[i]) and i + j < p:
                    tab[i][j] = old[i][j]
                else:
                    tab[i][j] = _dd_ddiv(self.observations, self.seq, tab, i, j)
        self.tab = tab
        self.bits.record(
            "divided_differences", [v for row in tab for v in row if v is not None])
        self.bits.record("inputs", list(self.observations.values()))

    # ------------------------------------------------------------------
    # 观测撤回
    # ------------------------------------------------------------------
    def forget(self, x, derivative_order: int = 0, cascade: bool = False) -> None:
        """撤回观测 (x,k)。

        * k=0（整块的函数值）撤回会移除该节点整个块；若该块还含高阶
          导数，需要 ``cascade=True`` 表示连同高阶导数一起撤回，否则
          抛 StructureError（拒绝静默丢失导数约束）。
        * k>0 时只能撤回该块当前最高一阶；撤回中间阶需要先按
          ``cascade=True`` 连更高阶一起撤回。
        撤回后调用 ``_reload(p)`` 复用前缀，仅重建后缀系数。
        """
        x = Fraction(x)
        k = int(derivative_order)
        key = (x, k)
        if key not in self.observations:
            raise UnknownObservationError(
                f"不存在观测 x={_fmt(x)} 的 {k} 阶导数")
        block_start = next(i for i, (z, _) in enumerate(self.seq) if z == x)
        block_end = block_start + 1
        while block_end < len(self.seq) and self.seq[block_end][0] == x:
            block_end += 1
        m = block_end - block_start  # 块长（0..m-1 阶）

        if k == 0:
            if m > 1 and not cascade:
                raise StructureError(
                    f"节点 x={_fmt(x)} 还带有 1..{m - 1} 阶导数；"
                    "撤回函数值会使它们失效，如确认请用 cascade=True")
            remove = list(range(block_start, block_end))
        else:
            if k != m - 1 and not cascade:
                raise StructureError(
                    f"节点 x={_fmt(x)} 存在更高阶 {k + 1}..{m - 1}；"
                    "撤回中间阶需 cascade=True 连高阶一起撤回")
            top = m - 1
            if k > top:
                raise UnknownObservationError("内部错误：阶数越界")
            remove = list(range(block_start + k, block_end))

        for idx in remove:
            self.observations.pop(self.seq[idx], None)
        p = remove[0]
        del self.seq[p:p + len(remove)]
        if self.seq:
            self._reload(p)
        else:
            self.tab = []

    # ------------------------------------------------------------------
    # 输出
    # ------------------------------------------------------------------
    @property
    def newton_coefficients(self) -> List[Fraction]:
        """Newton 对角线系数 c_j = f[z_0,...,z_j]。"""
        return [self.tab[0][j] for j in range(len(self.seq))]

    @property
    def nodes_in_order(self) -> List[Fraction]:
        return [z for (z, _) in self.seq]

    def newton_form(self) -> Tuple[List[Fraction], List[Fraction]]:
        """返回 (节点序列, Newton 系数)。重复节点即 Hermite 汇合节点。"""
        return self.nodes_in_order, self.newton_coefficients

    def polynomial(self) -> Poly:
        """展开为规范幂基多项式；无观测时为 0。"""
        if not self.seq:
            return Poly([0])
        return Poly.from_newton(self.nodes_in_order, self.newton_coefficients)

    # ------------------------------------------------------------------
    # 核验
    # ------------------------------------------------------------------
    def verify(self, poly: Optional[Poly] = None) -> Verification:
        """用全部观测核验：逐项检查 p^(k)(x) == y，全部精确相等才通过。"""
        if poly is None:
            poly = self.polynomial()
        residuals: Dict[Tuple[Fraction, int], Fraction] = {}
        for (x, k), y in self.observations.items():
            residuals[(x, k)] = poly.eval_derivative(x, k) - y
        ok = all(r == 0 for r in residuals.values())
        expected = max(0, len(self.seq) - 1)
        return Verification(ok, residuals, poly.degree, expected)

    def _hermite_system(self) -> Tuple[List[List[Fraction]], List[Fraction],
                                       List[Tuple[Fraction, int]], int]:
        """构造参考线性系统：未知幂基系数 a0..a_d，行按 (x,k) 约束展开。

        p^(k)(x) = sum_{t>=k} a_t * t!/(t-k)! * x^(t-k) = y。
        适用域：特征 0 有理数域，阶乘非零，公式恒成立。
        """
        d = max(0, len(self.seq) - 1)
        keys = sorted(self.observations.keys(), key=lambda t: (t[0], t[1]))
        A: List[List[Fraction]] = []
        b: List[Fraction] = []
        for (x, k) in keys:
            row = [Fraction(0)] * (d + 1)
            for t in range(k, d + 1):
                row[t] = Fraction(factorial(t), factorial(t - k)) * x ** (t - k)
            A.append(row)
            b.append(self.observations[(x, k)])
        return A, b, keys, d

    def reference_check(self) -> "ReferenceReport":
        """独立线性方程核验：解 Hermite-Vandermonde 系统并与差商结果比对。

        返回独立求解器得到的系数、是否与 Newton 展开逐项相同、以及见证。
        """
        if not self.seq:
            return ReferenceReport(True, Poly([0]), Poly([0]), True, None)
        A, b, keys, d = self._hermite_system()
        result = linear_solve(A, b)
        ref_poly = Poly(result.solution) if result.solution is not None else None
        mine = self.polynomial()
        self.bits.record("reference_matrix", [v for row in A for v in row] + b)
        if ref_poly is not None:
            self.bits.record("reference_solution", ref_poly.coeffs)
        match = ref_poly is not None and ref_poly == mine
        return ReferenceReport(result.consistent, ref_poly, mine, match,
                               result.witness, keys)


@dataclass
class ReferenceReport:
    consistent: bool
    reference: Optional[Poly]
    newton_based: Poly
    agree: bool
    witness: Optional[List[Fraction]]
    constraint_keys: List[Tuple[Fraction, int]] = field(default_factory=list)

    def witness_equation(self) -> Optional[str]:
        """把见证向量渲染成 '左端线性组合 = 右端组合' 的人读矛盾等式。

        左端恒为 0（见证与系数矩阵相乘为零行），右端为非零有理数。
        """
        if self.witness is None:
            return None
        left, right = [], []
        for w, (x, k) in zip(self.witness, self.constraint_keys):
            if w == 0:
                continue
            tag = f"p^({k})({_fmt(x)})" if k else f"p({_fmt(x)})"
            left.append(f"({_fmt(w)})*{tag}")
            right.append(f"({_fmt(w)})*y({_fmt(x)},{k})")
        return " + ".join(left) + " = 0,  但右端 " + " + ".join(right) + " != 0"
