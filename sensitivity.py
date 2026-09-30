"""受限查询表达式的主体级 L1 敏感度保守推导。

支持算子：sum、count、裁剪（Clip）、固定分组（GroupSum/GroupCount）、
连接（Join）、组合（Add/Scale）。仅使用 Python 标准库。

定义与约定（详见 README.md）：
- 邻接关系：主体级替换邻接。D 与 D' 相邻当且仅当它们仅在某个主体 s
  的全部行上不同（s 在所有表中的行可同时被替换/增删）。
- 贡献参数：每张表声明 max_rows_per_subject = m（None 表示无上限，
  一律拒绝）与值域 [value_lo, value_hi]。
- 敏感度：输出为标量时取 |Δ|；输出为固定键向量时取 L1 范数。
- 同一主体可能被多个子表达式引用：组合时按声明规则把各分支的
  贡献相加（顺序组合），绝不对算子局部取 max。

本模块只处理合成数据；有限域枚举核验（finite_check.py）是工程一致性
检查，不构成形式化隐私证明。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple, Union

Row = Tuple[str, str, float]  # (subject, group, value)
Database = Dict[str, List[Row]]  # 表名 -> 行列表
Vector = Dict[str, float]  # 固定分组键 -> 数值
QueryOutput = Union[float, Vector]


# ---------------------------------------------------------------- 表达式 AST


class Expr:
    """表达式基类。"""


class RelExpr(Expr):
    """关系表达式：产生行集合（主体, 组键, 数值）。"""


class QueryExpr(Expr):
    """查询表达式：产生标量或固定键向量。"""


@dataclass(frozen=True)
class Table(RelExpr):
    """基础表。max_rows_per_subject 为 None 表示无贡献上限（将被拒绝）。"""

    name: str
    max_rows_per_subject: Optional[int]
    value_lo: float = 0.0
    value_hi: float = 1.0
    groups: Tuple[str, ...] = ("g0",)


@dataclass(frozen=True)
class Clip(RelExpr):
    """逐行把数值裁剪进 [lo, hi]。"""

    child: RelExpr
    lo: float
    hi: float


@dataclass(frozen=True)
class Join(RelExpr):
    """按主体等值连接。连接行的数值 = 左值 * 右值，组键继承左侧。

    要求两侧值域下界非负（保证值域乘积区间有效）。
    """

    left: RelExpr
    right: RelExpr


@dataclass(frozen=True)
class Sum(QueryExpr):
    child: RelExpr


@dataclass(frozen=True)
class Count(QueryExpr):
    child: RelExpr


@dataclass(frozen=True)
class GroupSum(QueryExpr):
    """固定分组求和：输出为固定组键上的向量（缺失组记 0）。"""

    child: RelExpr


@dataclass(frozen=True)
class GroupCount(QueryExpr):
    """固定分组计数：输出为固定组键上的向量。"""

    child: RelExpr


@dataclass(frozen=True)
class Add(QueryExpr):
    """两个查询结果相加（标量或同键向量）。组合规则：敏感度相加。"""

    left: QueryExpr
    right: QueryExpr


@dataclass(frozen=True)
class Scale(QueryExpr):
    child: QueryExpr
    factor: float


# ---------------------------------------------------------------- 推导树


@dataclass
class Derivation:
    """一步保守推导：规则名、结论、该子表达式的界、子推导。"""

    rule: str
    conclusion: str
    bound: float
    children: Tuple["Derivation", ...] = ()

    def render(self, indent: int = 0) -> str:
        pad = "  " * indent
        lines = [f"{pad}[{self.rule}] {self.conclusion}  =>  {self.bound:g}"]
        for child in self.children:
            lines.append(child.render(indent + 1))
        return "\n".join(lines)


class UnboundedContributionError(Exception):
    """缺少贡献上限（或等价地无法给出有限界）时抛出，附带部分推导树。"""

    def __init__(self, message: str, derivation: Optional[Derivation] = None):
        super().__init__(message)
        self.derivation = derivation


@dataclass(frozen=True)
class RelInfo:
    """关系表达式的保守摘要：每主体行数上界、值域、固定组域。"""

    max_rows: int
    value_lo: float
    value_hi: float
    groups: Tuple[str, ...]


# ---------------------------------------------------------------- 敏感度分析


def analyze_rel(expr: RelExpr) -> Tuple[RelInfo, Derivation]:
    """分析关系表达式，返回 (摘要, 推导树)。无界贡献一律拒绝。"""
    if isinstance(expr, Table):
        if expr.max_rows_per_subject is None:
            deriv = Derivation(
                "TABLE-UNBOUNDED",
                f"表 {expr.name}: 未声明 max_rows_per_subject，主体贡献无界",
                float("inf"),
            )
            raise UnboundedContributionError(
                f"表 '{expr.name}' 缺少每主体行数上限：无贡献上限 => 无界，拒绝",
                deriv,
            )
        if expr.max_rows_per_subject < 0 or expr.value_lo > expr.value_hi:
            raise ValueError(f"表 '{expr.name}' 参数非法")
        info = RelInfo(
            expr.max_rows_per_subject, expr.value_lo, expr.value_hi, expr.groups
        )
        deriv = Derivation(
            "TABLE",
            f"表 {expr.name}: m={info.max_rows}, "
            f"值域[{info.value_lo:g},{info.value_hi:g}], 固定组{info.groups}",
            float(info.max_rows),
        )
        return info, deriv

    if isinstance(expr, Clip):
        info, child = analyze_rel(expr.child)
        lo = max(info.value_lo, expr.lo)
        hi = min(info.value_hi, expr.hi)
        if lo > hi:
            raise ValueError("裁剪区间与值域不相交")
        new = RelInfo(info.max_rows, lo, hi, info.groups)
        deriv = Derivation(
            "CLIP",
            f"裁剪至[{lo:g},{hi:g}]（与值域求交），行数界不变 m={info.max_rows}",
            float(info.max_rows),
            (child,),
        )
        return new, deriv

    if isinstance(expr, Join):
        linfo, lder = analyze_rel(expr.left)
        rinfo, rder = analyze_rel(expr.right)
        if linfo.value_lo < 0 or rinfo.value_lo < 0:
            raise ValueError("Join 要求两侧值域下界非负")
        # 联合传播：每主体连接行数 <= m_l * m_r；值域为乘积区间。
        m = linfo.max_rows * rinfo.max_rows
        lo = linfo.value_lo * rinfo.value_lo
        hi = linfo.value_hi * rinfo.value_hi
        new = RelInfo(m, lo, hi, linfo.groups)
        deriv = Derivation(
            "JOIN",
            f"连接: m={linfo.max_rows}*{rinfo.max_rows}={m}, "
            f"值域乘积[{lo:g},{hi:g}], 组键继承左侧",
            float(m),
            (lder, rder),
        )
        return new, deriv

    raise TypeError(f"未知关系表达式: {type(expr).__name__}")


def _amplitude(info: RelInfo) -> float:
    return max(abs(info.value_lo), abs(info.value_hi))


def analyze_query(expr: QueryExpr) -> Tuple[float, Derivation]:
    """分析查询表达式，返回 (L1 敏感度上界, 推导树)。"""
    if isinstance(expr, Sum):
        info, child = analyze_rel(expr.child)
        # 替换邻接：主体行数 0..m，故其行和落在 [m*lo, m*hi] 且含 0；
        # 标量 |Δ| 的紧界为 m*(max(hi,0)-min(lo,0))。lo>=0 时即经典的 m*b。
        amp = max(info.value_hi, 0.0) - min(info.value_lo, 0.0)
        bound = info.max_rows * amp
        deriv = Derivation(
            "SUM",
            f"sum: 界 = m*(max(hi,0)-min(lo,0)) = {info.max_rows}*{amp:g}",
            bound,
            (child,),
        )
        return bound, deriv

    if isinstance(expr, Count):
        info, child = analyze_rel(expr.child)
        bound = float(info.max_rows)
        deriv = Derivation(
            "COUNT", f"count: 界 = m = {info.max_rows}", bound, (child,)
        )
        return bound, deriv

    if isinstance(expr, GroupSum):
        info, child = analyze_rel(expr.child)
        amp = _amplitude(info)
        # 替换邻接 + 固定分组：被替换主体的旧行与新行可落入不相交的组，
        # 向量 L1 = 旧贡献 L1 + 新贡献 L1 <= 2*m*amp（跨组相加，非逐组取 max）。
        bound = 2 * info.max_rows * amp
        deriv = Derivation(
            "GROUP-SUM",
            f"固定分组 sum: 向量 L1 界 = 2*m*max(|lo|,|hi|) = "
            f"2*{info.max_rows}*{amp:g}（替换邻接下旧/新行可占不同组，跨组相加）",
            bound,
            (child,),
        )
        return bound, deriv

    if isinstance(expr, GroupCount):
        info, child = analyze_rel(expr.child)
        # 替换邻接：旧行集与新行集各至多 m 行，且可落入不同组 => 2*m。
        bound = float(2 * info.max_rows)
        deriv = Derivation(
            "GROUP-COUNT",
            f"固定分组 count: 向量 L1 界 = 2*m = 2*{info.max_rows}"
            "（替换邻接下旧/新行集分别计数）",
            bound,
            (child,),
        )
        return bound, deriv

    if isinstance(expr, Add):
        lbound, lder = analyze_query(expr.left)
        rbound, rder = analyze_query(expr.right)
        # 声明的组合规则：顺序组合 / 相关主体。同一主体可能同时驱动两侧，
        # 因此贡献相加，绝不取 max。
        bound = lbound + rbound
        deriv = Derivation(
            "ADD-COMPOSE",
            f"组合: 界 = {lbound:g}+{rbound:g}（相关主体贡献相加，非取 max）",
            bound,
            (lder, rder),
        )
        return bound, deriv

    if isinstance(expr, Scale):
        cbound, cder = analyze_query(expr.child)
        bound = abs(expr.factor) * cbound
        deriv = Derivation(
            "SCALE", f"缩放: 界 = |{expr.factor:g}|*{cbound:g}", bound, (cder,)
        )
        return bound, deriv

    raise TypeError(f"未知查询表达式: {type(expr).__name__}")


def analyze(expr: QueryExpr) -> Tuple[float, Derivation]:
    """顶层入口：返回 (敏感度上界, 完整推导树)。"""
    if not isinstance(expr, QueryExpr):
        raise TypeError("顶层表达式必须是查询表达式（Sum/Count/Group*/Add/Scale）")
    return analyze_query(expr)


# ---------------------------------------------------------------- 求值器


def eval_rel(expr: RelExpr, db: Database) -> List[Row]:
    """在合成数据上求关系表达式的行集合。"""
    if isinstance(expr, Table):
        return list(db.get(expr.name, []))
    if isinstance(expr, Clip):
        return [
            (s, g, min(max(v, expr.lo), expr.hi))
            for (s, g, v) in eval_rel(expr.child, db)
        ]
    if isinstance(expr, Join):
        left_rows = eval_rel(expr.left, db)
        right_rows = eval_rel(expr.right, db)
        by_subject: Dict[str, List[Tuple[str, float]]] = {}
        for (s, g, v) in right_rows:
            by_subject.setdefault(s, []).append((g, v))
        out: List[Row] = []
        for (s, g, v) in left_rows:
            for (_, rv) in by_subject.get(s, []):
                out.append((s, g, v * rv))
        return out
    raise TypeError(f"未知关系表达式: {type(expr).__name__}")


def eval_query(expr: QueryExpr, db: Database) -> QueryOutput:
    """在合成数据上求查询表达式的输出（标量或固定键向量）。"""
    if isinstance(expr, Sum):
        return float(sum(v for (_, _, v) in eval_rel(expr.child, db)))
    if isinstance(expr, Count):
        return float(len(eval_rel(expr.child, db)))
    if isinstance(expr, (GroupSum, GroupCount)):
        info, _ = analyze_rel(expr.child)
        out: Vector = {g: 0.0 for g in info.groups}
        for (_, g, v) in eval_rel(expr.child, db):
            out[g] += v if isinstance(expr, GroupSum) else 1.0
        return out
    if isinstance(expr, Add):
        lo = eval_query(expr.left, db)
        ro = eval_query(expr.right, db)
        if isinstance(lo, dict) and isinstance(ro, dict):
            keys = set(lo) | set(ro)
            return {k: lo.get(k, 0.0) + ro.get(k, 0.0) for k in keys}
        return float(lo) + float(ro)  # type: ignore[arg-type]
    if isinstance(expr, Scale):
        val = eval_query(expr.child, db)
        if isinstance(val, dict):
            return {k: expr.factor * v for k, v in val.items()}
        return expr.factor * val
    raise TypeError(f"未知查询表达式: {type(expr).__name__}")


def l1_distance(a: QueryOutput, b: QueryOutput) -> float:
    """输出的 L1 距离：标量取 |a-b|，固定键向量取逐键 |差| 之和。"""
    if isinstance(a, dict) and isinstance(b, dict):
        keys = set(a) | set(b)
        return float(sum(abs(a.get(k, 0.0) - b.get(k, 0.0)) for k in keys))
    return abs(float(a) - float(b))  # type: ignore[arg-type]
